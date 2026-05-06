#ifndef PARPARCHIK_CONFIG_H_
#define PARPARCHIK_CONFIG_H_

#include <cstdint>
#include <string>
#include <unordered_set>
#include <vector>

namespace parparchik {

struct Config {
  std::string aws_region = "us-east-1";
  std::string s3_endpoint;
  std::string registry_manifest_key = ".parparchik/files.json";
  std::vector<std::string> buckets;
  std::unordered_set<std::string> public_buckets;
  std::string host = "0.0.0.0";
  uint16_t port = 8080;

  bool HasCustomEndpoint() const { return !s3_endpoint.empty(); }
  bool IsBucketPublic(const std::string& bucket) const {
    return public_buckets.contains(bucket);
  }

  static Config FromEnv();
};

}  // namespace parparchik

#endif  // PARPARCHIK_CONFIG_H_
