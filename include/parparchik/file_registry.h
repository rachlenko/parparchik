#ifndef PARPARCHIK_FILE_REGISTRY_H_
#define PARPARCHIK_FILE_REGISTRY_H_

#include <mutex>
#include <optional>
#include <string>
#include <unordered_map>
#include <vector>

#include <nlohmann/json.hpp>

namespace parparchik {

struct FileEntry {
  std::string key;
  std::string bucket_name;
  std::string route;
  int64_t size;
  std::string last_modified;

  nlohmann::json ToJson() const;
};

class FileRegistry {
 public:
  explicit FileRegistry(std::vector<std::string> buckets);

  void LoadManifests(
      const std::vector<std::pair<std::string, std::string>>& manifests);

  void RegisterFile(const std::string& key, const std::string& bucket,
                    int64_t size, const std::string& last_modified = "");

  void MoveToBucket(const std::string& key, const std::string& bucket);

  void Remove(const std::string& key);

  std::optional<FileEntry> Lookup(const std::string& key) const;
  std::optional<FileEntry> LookupByRoute(const std::string& route) const;

  std::vector<FileEntry> ListAll() const;
  std::vector<FileEntry> ListByBucket(const std::string& bucket) const;

  nlohmann::json ManifestForBucket(const std::string& bucket) const;

  void Clear();

  const std::vector<std::string>& buckets() const { return buckets_; }

 private:
  int BucketPriority(const std::string& bucket) const;
  static std::string MakeRoute(const std::string& key,
                               const std::string& bucket);
  static std::optional<FileEntry> EntryFromJson(const nlohmann::json& item,
                                                const std::string& bucket);

  mutable std::mutex mu_;
  std::unordered_map<std::string, FileEntry> entries_;
  std::vector<std::string> buckets_;
};

}  // namespace parparchik

#endif  // PARPARCHIK_FILE_REGISTRY_H_
