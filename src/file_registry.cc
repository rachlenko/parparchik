#include "parparchik/file_registry.h"

namespace parparchik {

nlohmann::json FileEntry::ToJson() const {
  return {
      {"key", key},
      {"bucket", bucket_name},
      {"bucket_type",
       bucket_type == BucketType::kPublic ? "public" : "private"},
      {"route", route},
      {"size", size},
  };
}

FileRegistry::FileRegistry(const std::string& public_bucket,
                           const std::string& private_bucket)
    : public_bucket_(public_bucket), private_bucket_(private_bucket) {}

void FileRegistry::RegisterFile(const std::string& key, BucketType type,
                                int64_t size) {
  std::lock_guard lock(mu_);

  const std::string& bucket =
      (type == BucketType::kPublic) ? public_bucket_ : private_bucket_;

  entries_[key] = FileEntry{
      .key = key,
      .bucket_type = type,
      .bucket_name = bucket,
      .route = MakeRoute(key, type),
      .size = size,
  };
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
  return result;
}

void FileRegistry::Clear() {
  std::lock_guard lock(mu_);
  entries_.clear();
}

std::string FileRegistry::MakeRoute(const std::string& key, BucketType type) {
  std::string prefix =
      (type == BucketType::kPublic) ? "/public/" : "/private/";
  return prefix + key;
}

}  // namespace parparchik
