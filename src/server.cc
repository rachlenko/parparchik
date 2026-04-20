#include "parparchik/server.h"

#include <iostream>

#include <nlohmann/json.hpp>

namespace parparchik {

Server::Server(Config config) : config_(std::move(config)) {
  s3_ = std::make_unique<S3Client>(config_.aws_region, config_.s3_endpoint);
  registry_ = std::make_unique<FileRegistry>(config_.public_bucket,
                                             config_.private_bucket);
}

void Server::Run() {
  SyncRegistry();
  RegisterRoutes();

  std::cout << "parparchik listening on " << config_.host << ":"
            << config_.port << std::endl;
  http_.listen(config_.host, config_.port);
}

void Server::Stop() { http_.stop(); }

void Server::RegisterRoutes() {
  http_.Get("/status",
            [this](const httplib::Request& req, httplib::Response& res) {
              HandleStatus(req, res);
            });

  http_.Get("/list",
            [this](const httplib::Request& req, httplib::Response& res) {
              HandleList(req, res);
            });

  http_.Get("/update",
            [this](const httplib::Request& req, httplib::Response& res) {
              HandleUpdate(req, res);
            });

  http_.Get(R"(/public/(.+))",
            [this](const httplib::Request& req, httplib::Response& res) {
              HandleFileDownload(req, res);
            });

  http_.Get(R"(/private/(.+))",
            [this](const httplib::Request& req, httplib::Response& res) {
              HandleFileDownload(req, res);
            });
}

void Server::SyncRegistry() {
  registry_->Clear();

  auto public_objects = s3_->ListObjects(config_.public_bucket);
  for (const auto& obj : public_objects) {
    registry_->RegisterFile(obj.key, BucketType::kPublic, obj.size);
  }

  auto private_objects = s3_->ListObjects(config_.private_bucket);
  for (const auto& obj : private_objects) {
    auto existing = registry_->Lookup(obj.key);
    if (!existing.has_value()) {
      registry_->RegisterFile(obj.key, BucketType::kPrivate, obj.size);
    }
  }

  std::cout << "Registry synced: " << public_objects.size() << " public, "
            << private_objects.size() << " private objects" << std::endl;
}

void Server::HandleStatus(const httplib::Request& /*req*/,
                           httplib::Response& res) {
  nlohmann::json response = {
      {"status", "ok"},
      {"public_bucket", config_.public_bucket},
      {"private_bucket", config_.private_bucket},
      {"region", config_.aws_region},
      {"file_count", registry_->ListAll().size()},
  };

  res.set_content(response.dump(2), "application/json");
}

void Server::HandleList(const httplib::Request& /*req*/,
                        httplib::Response& res) {
  SyncRegistry();

  auto entries = registry_->ListAll();
  nlohmann::json files = nlohmann::json::array();
  for (const auto& entry : entries) {
    files.push_back(entry.ToJson());
  }

  nlohmann::json response = {
      {"count", entries.size()},
      {"files", files},
  };

  res.set_content(response.dump(2), "application/json");
}

void Server::HandleUpdate(const httplib::Request& req,
                          httplib::Response& res) {
  if (!req.has_param("filename")) {
    nlohmann::json error = {{"error", "missing 'filename' query parameter"}};
    res.status = 400;
    res.set_content(error.dump(2), "application/json");
    return;
  }

  std::string filename = req.get_param_value("filename");

  SyncRegistry();

  auto entry = registry_->Lookup(filename);
  if (!entry.has_value()) {
    nlohmann::json error = {{"error", "file not found"},
                            {"filename", filename}};
    res.status = 404;
    res.set_content(error.dump(2), "application/json");
    return;
  }

  nlohmann::json response = {
      {"message", "file located"},
      {"file", entry->ToJson()},
  };
  res.set_content(response.dump(2), "application/json");
}

void Server::HandleFileDownload(const httplib::Request& req,
                                httplib::Response& res) {
  std::string route = req.path;

  SyncRegistry();

  auto entry = registry_->LookupByRoute(route);
  if (!entry.has_value()) {
    nlohmann::json error = {{"error", "file not found"}, {"route", route}};
    res.status = 404;
    res.set_content(error.dump(2), "application/json");
    return;
  }

  if (entry->bucket_type == BucketType::kPublic) {
    std::string url =
        s3_->GetPublicUrl(entry->bucket_name, entry->key);
    res.set_redirect(url, 302);
  } else {
    std::string url =
        s3_->GeneratePresignedUrl(entry->bucket_name, entry->key, 3600);
    res.set_redirect(url, 302);
  }
}

}  // namespace parparchik
