#include "parparchik/server.h"

#include <iostream>
#include <stdexcept>
#include <unordered_set>

#include <nlohmann/json.hpp>

namespace parparchik {

Server::Server(Config config) : config_(std::move(config)) {
  s3_ = std::make_unique<S3Client>(config_.aws_region, config_.s3_endpoint);
  registry_ = std::make_unique<FileRegistry>(config_.buckets);
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

  http_.Post("/relocate",
             [this](const httplib::Request& req, httplib::Response& res) {
               HandleRelocate(req, res);
             });

  for (const auto& bucket : config_.buckets) {
    std::string pattern = "/" + bucket.name + "/(.+)";
    http_.Get(pattern,
              [this](const httplib::Request& req, httplib::Response& res) {
                HandleFileDownload(req, res);
              });
  }
}

void Server::SyncRegistry() {
  std::unordered_set<std::string> seen;
  std::unordered_set<std::string> duplicates;

  for (const auto& bucket : config_.buckets) {
    auto objects = s3_->ListObjects(bucket.name);
    for (const auto& obj : objects) {
      if (obj.key == bucket.manifest_key) {
        continue;
      }
      if (!seen.contains(obj.key)) {
        registry_->RegisterFile(obj.key, bucket.name, obj.size, obj.last_modified);
      } else {
        duplicates.insert(obj.key);
      }
      seen.insert(obj.key);
    }
  }

  for (const auto& entry : registry_->ListAll()) {
    if (!seen.contains(entry.key)) {
      registry_->Remove(entry.key);
    }
  }

  metrics_->SetDuplicateFiles(static_cast<int>(duplicates.size()));
  PersistRegistryManifests();
  RefreshMetrics();

  std::cout << "Registry synced across " << config_.buckets.size()
            << " buckets (" << duplicates.size() << " duplicates)"
            << std::endl;
}

void Server::LoadRegistryManifests() {
  std::vector<std::pair<std::string, std::string>> manifests;
  bool all_present = true;

  for (const auto& bucket : config_.buckets) {
    std::string content;
    bool found = s3_->TryGetObjectContent(
        bucket.name, bucket.manifest_key, &content);
    manifests.emplace_back(bucket.name, content);
    if (!found) {
      all_present = false;
    }
  }

  registry_->LoadManifests(manifests);
  if (!all_present) {
    BackfillRegistryFromBuckets();
  }

  ReconcileRegistryWithBuckets();
  PersistRegistryManifests();
}

void Server::BackfillRegistryFromBuckets() {
  std::unordered_set<std::string> seen;

  for (const auto& bucket : config_.buckets) {
    for (const auto& object : s3_->ListObjects(bucket.name)) {
      if (object.key == bucket.manifest_key) {
        continue;
      }
      if (!seen.contains(object.key)) {
        registry_->RegisterFile(object.key, bucket.name, object.size,
                                object.last_modified);
      }
      seen.insert(object.key);
    }
  }
}

void Server::ReconcileRegistryWithBuckets() {
  int duplicates = 0;
  for (const auto& entry : registry_->ListAll()) {
    int bucket_count = 0;
    bool registered = false;
    for (const auto& bucket : config_.buckets) {
      const auto object = s3_->HeadObject(bucket.name, entry.key);
      if (object.has_value()) {
        if (!registered) {
          registry_->RegisterFile(entry.key, bucket.name, object->size,
                                  object->last_modified);
          registered = true;
        }
        ++bucket_count;
      }
    }
    if (!registered) {
      registry_->Remove(entry.key);
    }
    if (bucket_count > 1) {
      ++duplicates;
    }
  }
  metrics_->SetDuplicateFiles(duplicates);
}

void Server::PersistRegistryManifests() {
  for (const auto& bucket : config_.buckets) {
    const std::string manifest =
        registry_->ManifestForBucket(bucket.name).dump(2);
    if (!s3_->PutObjectContent(bucket.name, bucket.manifest_key,
                               manifest, "application/json")) {
      throw std::runtime_error(
          "Failed to write registry manifest to S3 bucket: " + bucket.name);
    }
  }
}

std::optional<FileEntry> Server::ResolveMissingFile(const std::string& key) {
  for (const auto& bucket : config_.buckets) {
    const auto object = s3_->HeadObject(bucket.name, key);
    if (object.has_value()) {
      registry_->RegisterFile(key, bucket.name, object->size, object->last_modified);
      PersistRegistryManifests();
      RefreshMetrics();
      return registry_->Lookup(key);
    }
  }

  registry_->Remove(key);
  PersistRegistryManifests();
  RefreshMetrics();
  return std::nullopt;
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

  for (const auto& bucket : config_.buckets) {
    const std::string prefix = "/" + bucket.name + "/";
    if (route.starts_with(prefix)) {
      return ResolveMissingFile(route.substr(prefix.size()));
    }
  }
  return std::nullopt;
}

void Server::RefreshMetrics() { metrics_->ObserveFiles(registry_->ListAll()); }

void Server::HandleStatus(const httplib::Request& /*req*/,
                           httplib::Response& res) {
  nlohmann::json bucket_list = nlohmann::json::array();
  for (const auto& bucket : config_.buckets) {
    bucket_list.push_back({
        {"name", bucket.name},
        {"manifest_key", bucket.manifest_key},
        {"public", bucket.is_public},
    });
  }

  nlohmann::json response = {
      {"status", "ok"},
      {"buckets", bucket_list},
      {"region", config_.aws_region},
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

void Server::HandleRelocate(const httplib::Request& req,
                            httplib::Response& res) {
  if (!req.has_param("filename")) {
    nlohmann::json error = {{"error", "missing 'filename' query parameter"}};
    res.status = 400;
    res.set_content(error.dump(2), "application/json");
    return;
  }

  const std::string filename = req.get_param_value("filename");

  // Check all buckets; last match wins (reverse priority for relocate).
  std::string target_bucket;
  S3Object target_object;
  int found_count = 0;

  for (const auto& bucket : config_.buckets) {
    const auto object = s3_->HeadObject(bucket.name, filename);
    if (object.has_value()) {
      target_bucket = bucket.name;
      target_object = *object;
      ++found_count;
    }
  }

  if (found_count == 0) {
    registry_->Remove(filename);
    try {
      PersistRegistryManifests();
    } catch (...) {}
    RefreshMetrics();
    nlohmann::json response = {
        {"status", "fail"},
        {"error", "file not found in any bucket"},
        {"filename", filename},
    };
    res.status = 404;
    res.set_content(response.dump(2), "application/json");
    return;
  }

  const auto previous = registry_->Lookup(filename);

  if (previous.has_value() && previous->bucket_name != target_bucket) {
    registry_->MoveToBucket(filename, target_bucket);
  }
  registry_->RegisterFile(filename, target_bucket, target_object.size,
                           target_object.last_modified);

  try {
    PersistRegistryManifests();
  } catch (const std::exception& e) {
    nlohmann::json response = {
        {"status", "fail"},
        {"error", "failed to persist manifests"},
        {"filename", filename},
    };
    res.status = 500;
    res.set_content(response.dump(2), "application/json");
    return;
  }
  RefreshMetrics();

  auto entry = registry_->Lookup(filename);
  nlohmann::json response = {{"status", "ok"}, {"file", entry->ToJson()}};
  if (previous.has_value() && previous->bucket_name != target_bucket) {
    response["relocated_from"] = previous->bucket_name;
    response["relocated_to"] = target_bucket;
  }
  if (found_count > 1) {
    response["duplicate"] = true;
    response["note"] =
        "file exists in multiple buckets, last configured bucket takes "
        "precedence";
  }
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

  if (config_.IsBucketPublic(entry->bucket_name)) {
    std::string url = s3_->GetPublicUrl(entry->bucket_name, entry->key);
    res.set_redirect(url, 302);
  } else {
    std::string url =
        s3_->GeneratePresignedUrl(entry->bucket_name, entry->key, 3600);
    res.set_redirect(url, 302);
  }
}

}  // namespace parparchik
