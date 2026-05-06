#ifndef PARPARCHIK_FILE_REGISTRY_H_
#define PARPARCHIK_FILE_REGISTRY_H_

#include <mutex>
#include <optional>
#include <string>
#include <unordered_map>
#include <vector>

#include <nlohmann/json.hpp>

namespace parparchik {

enum class BucketType { kPrivate, kPublic };

struct FileEntry {
  std::string key;
  BucketType bucket_type;
  std::string bucket_name;
  std::string route;
  int64_t size;
  std::string last_modified;

  nlohmann::json ToJson() const;
};

class FileRegistry {
 public:
  FileRegistry(const std::string& public_bucket,
               const std::string& private_bucket);

  void LoadManifests(const std::string& public_manifest,
                     const std::string& private_manifest);

  void RegisterFile(const std::string& key, BucketType type, int64_t size,
                    const std::string& last_modified = "");

  void MoveToPublic(const std::string& key);
  void MoveToPrivate(const std::string& key);

  void Remove(const std::string& key);

  std::optional<FileEntry> Lookup(const std::string& key) const;
  std::optional<FileEntry> LookupByRoute(const std::string& route) const;

  std::vector<FileEntry> ListAll() const;
  std::vector<FileEntry> ListByBucket(BucketType type) const;

  nlohmann::json ManifestForBucket(BucketType type) const;

  void Clear();

 private:
  static std::string BucketTypeToString(BucketType type);
  static BucketType BucketTypeFromString(const std::string& value);
  static std::string MakeRoute(const std::string& key, BucketType type);
  static std::optional<FileEntry> EntryFromJson(
      const nlohmann::json& item, BucketType type,
      const std::string& public_bucket, const std::string& private_bucket);

  mutable std::mutex mu_;
  std::unordered_map<std::string, FileEntry> entries_;
  std::string public_bucket_;
  std::string private_bucket_;
};

}  // namespace parparchik

#endif  // PARPARCHIK_FILE_REGISTRY_H_
