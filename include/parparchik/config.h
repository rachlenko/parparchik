#ifndef PARPARCHIK_CONFIG_H_
#define PARPARCHIK_CONFIG_H_

#include <cstdint>
#include <string>
#include <unordered_set>
#include <vector>

namespace parparchik {

struct BucketConfig {
  std::string name;
  std::string manifest_key;
  bool is_public;
};

struct Config {
  std::string aws_region = "us-east-1";
  std::string s3_endpoint;
  std::vector<BucketConfig> buckets;
  std::string host = "0.0.0.0";
  uint16_t port = 8080;

  bool HasCustomEndpoint() const { return !s3_endpoint.empty(); }
  bool IsBucketPublic(const std::string& bucket_name) const {
    for (const auto& bucket : buckets) {
      if (bucket.name == bucket_name) {
        return bucket.is_public;
      }
    }
    return false;
  }

  static Config FromEnv();
};

}  // namespace parparchik

#endif  // PARPARCHIK_CONFIG_H_
