#include "parparchik/file_registry.h"

#include <algorithm>
#include <iostream>

namespace parparchik {

nlohmann::json FileEntry::ToJson() const {
  return {
      {"key", key},
      {"bucket", bucket_name},
      {"route", route},
      {"size", size},
      {"last_modified", last_modified},
  };
}

FileRegistry::FileRegistry(std::vector<BucketConfig> buckets)
    : buckets_(std::move(buckets)) {}

int FileRegistry::BucketPriority(const std::string& bucket) const {
  for (int i = 0; i < static_cast<int>(buckets_.size()); ++i) {
    if (buckets_[i].name == bucket) {
      return i;
    }
  }
  return static_cast<int>(buckets_.size());
}

void FileRegistry::LoadManifests(
    const std::vector<std::pair<std::string, std::string>>& manifests) {
  std::unordered_map<std::string, FileEntry> loaded;

  // Load in reverse priority order so higher-priority buckets overwrite.
  for (auto it = manifests.rbegin(); it != manifests.rend(); ++it) {
    const auto& [bucket, manifest] = *it;
    if (manifest.empty()) {
      continue;
    }

    nlohmann::json parsed;
    try {
      parsed = nlohmann::json::parse(manifest);
    } catch (const nlohmann::json::exception& e) {
      std::cerr << "Ignoring invalid " << bucket
                << " registry manifest: " << e.what() << std::endl;
      continue;
    }

    nlohmann::json files = parsed;
    if (parsed.is_object() && parsed.contains("files")) {
      files = parsed["files"];
    }
    if (!files.is_array()) {
      std::cerr << "Ignoring " << bucket
                << " registry manifest: no files array" << std::endl;
      continue;
    }

    for (const auto& item : files) {
      auto entry = EntryFromJson(item, bucket);
      if (!entry.has_value() || entry->key.empty()) {
        continue;
      }
      loaded[entry->key] = *entry;
    }
  }

  std::lock_guard lock(mu_);
  entries_ = std::move(loaded);
}

void FileRegistry::RegisterFile(const std::string& key,
                                const std::string& bucket, int64_t size,
                                const std::string& last_modified) {
  FileEntry entry{
      .key = key,
      .bucket_name = bucket,
      .route = MakeRoute(key, bucket),
      .size = size,
      .last_modified = last_modified,
  };

  std::lock_guard lock(mu_);
  auto existing = entries_.find(key);
  if (existing != entries_.end()) {
    int existing_pri = BucketPriority(existing->second.bucket_name);
    int new_pri = BucketPriority(bucket);
    if (existing_pri < new_pri) {
      return;
    }
  }
  entries_[key] = entry;
}

void FileRegistry::MoveToBucket(const std::string& key,
                                const std::string& bucket) {
  std::lock_guard lock(mu_);
  auto it = entries_.find(key);
  if (it == entries_.end()) {
    return;
  }
  it->second.bucket_name = bucket;
  it->second.route = MakeRoute(key, bucket);
}

void FileRegistry::Remove(const std::string& key) {
  std::lock_guard lock(mu_);
  entries_.erase(key);
}

std::optional<FileEntry> FileRegistry::Lookup(const std::string& key) const {
  std::lock_guard lock(mu_);
  auto it = entries_.find(key);
  if (it == entries_.end()) {
    return std::nullopt;
  }
  return it->second;
}

std::optional<FileEntry> FileRegistry::LookupByRoute(
    const std::string& route) const {
  std::lock_guard lock(mu_);
  for (const auto& [_, entry] : entries_) {
    if (entry.route == route) {
      return entry;
    }
  }
  return std::nullopt;
}

std::vector<FileEntry> FileRegistry::ListAll() const {
  std::lock_guard lock(mu_);
  std::vector<FileEntry> result;
  result.reserve(entries_.size());
  for (const auto& [_, entry] : entries_) {
    result.push_back(entry);
  }
  std::sort(result.begin(), result.end(),
            [](const FileEntry& lhs, const FileEntry& rhs) {
              return lhs.key < rhs.key;
            });
  return result;
}

std::vector<FileEntry> FileRegistry::ListByBucket(
    const std::string& bucket) const {
  std::lock_guard lock(mu_);
  std::vector<FileEntry> result;
  for (const auto& [_, entry] : entries_) {
    if (entry.bucket_name == bucket) {
      result.push_back(entry);
    }
  }
  std::sort(result.begin(), result.end(),
            [](const FileEntry& lhs, const FileEntry& rhs) {
              return lhs.key < rhs.key;
            });
  return result;
}

nlohmann::json FileRegistry::ManifestForBucket(
    const std::string& bucket) const {
  nlohmann::json files = nlohmann::json::array();
  for (const auto& entry : ListByBucket(bucket)) {
    files.push_back(entry.ToJson());
  }
  return {
      {"version", 1},
      {"bucket", bucket},
      {"files", files},
  };
}

void FileRegistry::Clear() {
  std::lock_guard lock(mu_);
  entries_.clear();
}

std::string FileRegistry::MakeRoute(const std::string& key,
                                    const std::string& bucket) {
  return "/" + bucket + "/" + key;
}

std::optional<FileEntry> FileRegistry::EntryFromJson(
    const nlohmann::json& item, const std::string& bucket) {
  if (!item.is_object()) {
    return std::nullopt;
  }

  const std::string key = item.value("key", "");
  if (key.empty()) {
    return std::nullopt;
  }

  const std::string entry_bucket = item.value("bucket", bucket);
  const std::string actual_bucket = entry_bucket.empty() ? bucket : entry_bucket;

  return FileEntry{
      .key = key,
      .bucket_name = actual_bucket,
      .route = MakeRoute(key, actual_bucket),
      .size = item.value("size", static_cast<int64_t>(0)),
      .last_modified = item.value("last_modified", std::string{}),
  };
}

}  // namespace parparchik
