#include "parparchik/file_registry.h"

#include <algorithm>
#include <iostream>

namespace parparchik {

namespace {

std::string BucketNameForType(BucketType type,
                              const std::string& public_bucket,
                              const std::string& private_bucket) {
  return type == BucketType::kPublic ? public_bucket : private_bucket;
}

}  // namespace

nlohmann::json FileEntry::ToJson() const {
  return {
      {"key", key},
      {"bucket", bucket_name},
      {"bucket_type", bucket_type == BucketType::kPublic ? "public" : "private"},
      {"route", route},
      {"size", size},
      {"last_modified", last_modified},
  };
}

FileRegistry::FileRegistry(const std::string& public_bucket,
                           const std::string& private_bucket)
    : public_bucket_(public_bucket), private_bucket_(private_bucket) {}

void FileRegistry::LoadManifests(const std::string& public_manifest,
                                 const std::string& private_manifest) {
  std::unordered_map<std::string, FileEntry> loaded;

  auto load_one = [this, &loaded](const std::string& manifest,
                                  BucketType type) {
    if (manifest.empty()) {
      return;
    }

    nlohmann::json parsed;
    try {
      parsed = nlohmann::json::parse(manifest);
    } catch (const nlohmann::json::exception& e) {
      std::cerr << "Ignoring invalid " << BucketTypeToString(type)
                << " registry manifest: " << e.what() << std::endl;
      return;
    }

    nlohmann::json files = parsed;
    if (parsed.is_object() && parsed.contains("files")) {
      files = parsed["files"];
    }
    if (!files.is_array()) {
      std::cerr << "Ignoring " << BucketTypeToString(type)
                << " registry manifest because it does not contain a files array"
                << std::endl;
      return;
    }

    for (const auto& item : files) {
      auto entry = EntryFromJson(item, type, public_bucket_, private_bucket_);
      if (!entry.has_value() || entry->key.empty()) {
        continue;
      }

      auto existing = loaded.find(entry->key);
      if (existing == loaded.end() || entry->bucket_type == BucketType::kPublic) {
        loaded[entry->key] = *entry;
      }
    }
  };

  load_one(private_manifest, BucketType::kPrivate);
  load_one(public_manifest, BucketType::kPublic);

  std::lock_guard lock(mu_);
  entries_ = std::move(loaded);
}

void FileRegistry::RegisterFile(const std::string& key, BucketType type,
                                int64_t size,
                                const std::string& last_modified) {
  const std::string bucket = BucketNameForType(type, public_bucket_, private_bucket_);

  FileEntry entry{
      .key = key,
      .bucket_type = type,
      .bucket_name = bucket,
      .route = MakeRoute(key, type),
      .size = size,
      .last_modified = last_modified,
  };

  std::lock_guard lock(mu_);
  auto existing = entries_.find(key);
  if (existing != entries_.end() && existing->second.bucket_type == BucketType::kPublic &&
      type == BucketType::kPrivate) {
    return;
  }
  entries_[key] = entry;
}

void FileRegistry::MoveToPublic(const std::string& key) {
  std::lock_guard lock(mu_);

  auto it = entries_.find(key);
  if (it == entries_.end()) {
    return;
  }

  it->second.bucket_type = BucketType::kPublic;
  it->second.bucket_name = public_bucket_;
  it->second.route = MakeRoute(key, BucketType::kPublic);
}

void FileRegistry::MoveToPrivate(const std::string& key) {
  std::lock_guard lock(mu_);

  auto it = entries_.find(key);
  if (it == entries_.end()) {
    return;
  }

  it->second.bucket_type = BucketType::kPrivate;
  it->second.bucket_name = private_bucket_;
  it->second.route = MakeRoute(key, BucketType::kPrivate);
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
  std::sort(result.begin(), result.end(), [](const FileEntry& lhs,
                                             const FileEntry& rhs) {
    return lhs.key < rhs.key;
  });
  return result;
}

std::vector<FileEntry> FileRegistry::ListByBucket(BucketType type) const {
  std::lock_guard lock(mu_);

  std::vector<FileEntry> result;
  for (const auto& [_, entry] : entries_) {
    if (entry.bucket_type == type) {
      result.push_back(entry);
    }
  }
  std::sort(result.begin(), result.end(), [](const FileEntry& lhs,
                                             const FileEntry& rhs) {
    return lhs.key < rhs.key;
  });
  return result;
}

nlohmann::json FileRegistry::ManifestForBucket(BucketType type) const {
  nlohmann::json files = nlohmann::json::array();
  for (const auto& entry : ListByBucket(type)) {
    files.push_back(entry.ToJson());
  }

  return {
      {"version", 1},
      {"bucket_type", BucketTypeToString(type)},
      {"files", files},
  };
}

void FileRegistry::Clear() {
  std::lock_guard lock(mu_);
  entries_.clear();
}

std::string FileRegistry::BucketTypeToString(BucketType type) {
  return type == BucketType::kPublic ? "public" : "private";
}

BucketType FileRegistry::BucketTypeFromString(const std::string& value) {
  return value == "public" ? BucketType::kPublic : BucketType::kPrivate;
}

std::string FileRegistry::MakeRoute(const std::string& key, BucketType type) {
  std::string prefix =
      (type == BucketType::kPublic) ? "/public/" : "/private/";
  return prefix + key;
}

std::optional<FileEntry> FileRegistry::EntryFromJson(
    const nlohmann::json& item, BucketType type,
    const std::string& public_bucket, const std::string& private_bucket) {
  if (!item.is_object()) {
    return std::nullopt;
  }

  const std::string key = item.value("key", "");
  if (key.empty()) {
    return std::nullopt;
  }

  if (item.contains("bucket_type")) {
    type = BucketTypeFromString(item.value("bucket_type", BucketTypeToString(type)));
  }

  const std::string bucket = item.value(
      "bucket", BucketNameForType(type, public_bucket, private_bucket));

  return FileEntry{
      .key = key,
      .bucket_type = type,
      .bucket_name = bucket.empty() ? BucketNameForType(type, public_bucket, private_bucket)
                                   : bucket,
      .route = item.value("route", MakeRoute(key, type)),
      .size = item.value("size", static_cast<int64_t>(0)),
      .last_modified = item.value("last_modified", std::string{}),
  };
}

}  // namespace parparchik
