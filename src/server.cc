#include "parparchik/server.h"

#include <iostream>
#include <stdexcept>
#include <unordered_set>

#include <nlohmann/json.hpp>

namespace parparchik {

Server::Server(Config config) : config_(std::move(config)) {
  s3_ = std::make_unique<S3Client>(config_.aws_region, config_.s3_endpoint);
  registry_ = std::make_unique<FileRegistry>(config_.public_bucket,
                                             config_.private_bucket);
  metrics_ = std::make_unique<Metrics>();
}

void Server::Run() {
  ready_.store(false);
  LoadRegistryManifests();
  RefreshMetrics();
  ready_.store(true);
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

  http_.Get("/redines",
            [this](const httplib::Request& req, httplib::Response& res) {
              HandleReadiness(req, res);
            });

  http_.Get("/readiness",
            [this](const httplib::Request& req, httplib::Response& res) {
              HandleReadiness(req, res);
            });

  http_.Get("/helthcheck",
            [this](const httplib::Request& req, httplib::Response& res) {
              HandleHealthcheck(req, res);
            });

  http_.Get("/healthcheck",
            [this](const httplib::Request& req, httplib::Response& res) {
              HandleHealthcheck(req, res);
            });

  http_.Get("/list",
            [this](const httplib::Request& req, httplib::Response& res) {
              HandleList(req, res);
            });

  http_.Get("/metrics",
            [this](const httplib::Request& req, httplib::Response& res) {
              HandleMetrics(req, res);
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
  std::unordered_set<std::string> seen;

  auto public_objects = s3_->ListObjects(config_.public_bucket);
  for (const auto& obj : public_objects) {
    if (obj.key == config_.registry_manifest_key) {
      continue;
    }
    seen.insert(obj.key);
    registry_->RegisterFile(obj.key, BucketType::kPublic, obj.size,
                            obj.last_modified);
  }

  auto private_objects = s3_->ListObjects(config_.private_bucket);
  for (const auto& obj : private_objects) {
    if (obj.key == config_.registry_manifest_key) {
      continue;
    }
    if (!seen.contains(obj.key)) {
      registry_->RegisterFile(obj.key, BucketType::kPrivate, obj.size,
                              obj.last_modified);
    }
    seen.insert(obj.key);
  }

  for (const auto& entry : registry_->ListAll()) {
    if (!seen.contains(entry.key)) {
      registry_->Remove(entry.key);
    }
  }

  PersistRegistryManifests();
  RefreshMetrics();

  std::cout << "Registry synced: " << public_objects.size() << " public, "
            << private_objects.size() << " private objects" << std::endl;
}

void Server::LoadRegistryManifests() {
  std::string public_manifest;
  std::string private_manifest;

  const bool has_public_manifest = s3_->TryGetObjectContent(
      config_.public_bucket, config_.registry_manifest_key, &public_manifest);
  const bool has_private_manifest = s3_->TryGetObjectContent(
      config_.private_bucket, config_.registry_manifest_key, &private_manifest);

  registry_->LoadManifests(public_manifest, private_manifest);
  if (!has_public_manifest || !has_private_manifest) {
    BackfillRegistryFromBuckets();
  }

  ReconcileRegistryWithBuckets();
  PersistRegistryManifests();
}

void Server::BackfillRegistryFromBuckets() {
  std::unordered_set<std::string> public_keys;

  for (const auto& object : s3_->ListObjects(config_.public_bucket)) {
    if (object.key == config_.registry_manifest_key) {
      continue;
    }
    public_keys.insert(object.key);
    registry_->RegisterFile(object.key, BucketType::kPublic, object.size,
                            object.last_modified);
  }

  for (const auto& object : s3_->ListObjects(config_.private_bucket)) {
    if (object.key == config_.registry_manifest_key ||
        public_keys.contains(object.key)) {
      continue;
    }
    registry_->RegisterFile(object.key, BucketType::kPrivate, object.size,
                            object.last_modified);
  }
}

void Server::ReconcileRegistryWithBuckets() {
  for (const auto& entry : registry_->ListAll()) {
    const auto public_object = s3_->HeadObject(config_.public_bucket, entry.key);
    const auto private_object = s3_->HeadObject(config_.private_bucket, entry.key);

    if (public_object.has_value()) {
      registry_->RegisterFile(entry.key, BucketType::kPublic,
                              public_object->size,
                              public_object->last_modified);
    } else if (private_object.has_value()) {
      registry_->RegisterFile(entry.key, BucketType::kPrivate,
                              private_object->size,
                              private_object->last_modified);
    } else {
      registry_->Remove(entry.key);
    }
  }
}

void Server::PersistRegistryManifests() {
  const std::string public_manifest =
      registry_->ManifestForBucket(BucketType::kPublic).dump(2);
  const std::string private_manifest =
      registry_->ManifestForBucket(BucketType::kPrivate).dump(2);

  if (!s3_->PutObjectContent(config_.public_bucket, config_.registry_manifest_key,
                             public_manifest, "application/json")) {
    throw std::runtime_error("Failed to write public registry manifest to S3");
  }
  if (!s3_->PutObjectContent(config_.private_bucket,
                             config_.registry_manifest_key, private_manifest,
                             "application/json")) {
    throw std::runtime_error("Failed to write private registry manifest to S3");
  }
}

std::optional<FileEntry> Server::ResolveMissingFile(const std::string& key) {
  const auto public_object = s3_->HeadObject(config_.public_bucket, key);
  const auto private_object = s3_->HeadObject(config_.private_bucket, key);

  if (!public_object.has_value() && !private_object.has_value()) {
    registry_->Remove(key);
    PersistRegistryManifests();
    RefreshMetrics();
    return std::nullopt;
  }

  const BucketType type = public_object.has_value() ? BucketType::kPublic
                                                    : BucketType::kPrivate;
  const auto& object = public_object.has_value() ? *public_object
                                                 : *private_object;

  registry_->RegisterFile(key, type, object.size, object.last_modified);
  PersistRegistryManifests();
  RefreshMetrics();
  return registry_->Lookup(key);
}

std::optional<FileEntry> Server::ResolveRoute(const std::string& route) {
  auto entry = registry_->LookupByRoute(route);
  if (entry.has_value()) {
    const bool exists = s3_->ObjectExists(entry->bucket_name, entry->key);
    if (exists) {
      return entry;
    }
    return ResolveMissingFile(entry->key);
  }

  const std::string public_prefix = "/public/";
  const std::string private_prefix = "/private/";
  if (route.starts_with(public_prefix)) {
    return ResolveMissingFile(route.substr(public_prefix.size()));
  }
  if (route.starts_with(private_prefix)) {
    return ResolveMissingFile(route.substr(private_prefix.size()));
  }
  return std::nullopt;
}

void Server::RefreshMetrics() { metrics_->ObserveFiles(registry_->ListAll()); }

void Server::HandleStatus(const httplib::Request& /*req*/,
                           httplib::Response& res) {
  nlohmann::json response = {
      {"status", "ok"},
      {"public_bucket", config_.public_bucket},
      {"private_bucket", config_.private_bucket},
      {"region", config_.aws_region},
      {"registry_manifest_key", config_.registry_manifest_key},
      {"ready", ready_.load()},
      {"file_count", registry_->ListAll().size()},
  };

  res.set_content(response.dump(2), "application/json");
}

void Server::HandleReadiness(const httplib::Request& /*req*/,
                             httplib::Response& res) {
  const bool ready = ready_.load();
  nlohmann::json response = {
      {"status", ready ? "ready" : "not_ready"},
      {"ready", ready},
      {"file_count", registry_->ListAll().size()},
  };

  if (!ready) {
    res.status = 503;
  }
  res.set_content(response.dump(2), "application/json");
}

void Server::HandleHealthcheck(const httplib::Request& /*req*/,
                               httplib::Response& res) {
  nlohmann::json response = {
      {"status", "ok"},
      {"ready", ready_.load()},
  };

  res.set_content(response.dump(2), "application/json");
}

void Server::HandleMetrics(const httplib::Request& /*req*/,
                           httplib::Response& res) {
  RefreshMetrics();
  res.set_content(metrics_->Render(), "text/plain; version=0.0.4");
}

void Server::HandleList(const httplib::Request& /*req*/,
                        httplib::Response& res) {
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

  auto entry = registry_->Lookup(filename);
  if (!entry.has_value()) {
    entry = ResolveMissingFile(filename);
  }
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

  auto entry = ResolveRoute(route);
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
