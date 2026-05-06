#include "parparchik/config.h"

#include <cstdlib>
#include <sstream>
#include <stdexcept>

namespace parparchik {

namespace {

std::string GetEnvOr(const char* name, const std::string& default_value) {
  const char* value = std::getenv(name);
  if (value == nullptr || value[0] == '\0') {
    return default_value;
  }
  return value;
}

}  // namespace

Config Config::FromEnv() {
  Config config;
  config.aws_region = GetEnvOr("AWS_REGION", "us-east-1");
  config.s3_endpoint = GetEnvOr("S3_ENDPOINT", "");
  config.registry_manifest_key =
      GetEnvOr("PARPARCHIK_REGISTRY_MANIFEST_KEY", ".parparchik/files.json");
  config.host = GetEnvOr("PARPARCHIK_HOST", "0.0.0.0");

  std::string port_str = GetEnvOr("PARPARCHIK_PORT", "8080");
  config.port = static_cast<uint16_t>(std::stoi(port_str));

  std::string buckets_str = GetEnvOr("PARPARCHIK_BUCKETS", "");
  if (!buckets_str.empty()) {
    std::istringstream ss(buckets_str);
    std::string token;
    while (std::getline(ss, token, ',')) {
      const std::string suffix = ":public";
      if (token.size() > suffix.size() &&
          token.compare(token.size() - suffix.size(), suffix.size(), suffix) ==
              0) {
        std::string name = token.substr(0, token.size() - suffix.size());
        config.buckets.push_back(name);
        config.public_buckets.insert(name);
      } else {
        config.buckets.push_back(token);
      }
    }
  } else {
    const char* pub = std::getenv("PARPARCHIK_PUBLIC_BUCKET");
    const char* priv = std::getenv("PARPARCHIK_PRIVATE_BUCKET");
    if (!pub || pub[0] == '\0' || !priv || priv[0] == '\0') {
      throw std::runtime_error(
          "Set PARPARCHIK_BUCKETS or both PARPARCHIK_PUBLIC_BUCKET and "
          "PARPARCHIK_PRIVATE_BUCKET");
    }
    config.buckets.push_back(pub);
    config.public_buckets.insert(pub);
    config.buckets.push_back(priv);
  }

  if (config.buckets.empty()) {
    throw std::runtime_error("At least one bucket must be configured");
  }

  return config;
}

}  // namespace parparchik
