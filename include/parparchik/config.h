#ifndef PARPARCHIK_CONFIG_H_
#define PARPARCHIK_CONFIG_H_

#include <cstdint>
#include <string>

namespace parparchik {

struct Config {
  std::string aws_region = "us-east-1";
  std::string s3_endpoint;
  std::string public_bucket;
  std::string private_bucket;
  std::string host = "0.0.0.0";
  uint16_t port = 8080;

  bool HasCustomEndpoint() const { return !s3_endpoint.empty(); }

  static Config FromEnv();
};

}  // namespace parparchik

#endif  // PARPARCHIK_CONFIG_H_
