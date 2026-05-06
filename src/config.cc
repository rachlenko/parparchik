#include "parparchik/config.h"

#include <cstdlib>
#include <stdexcept>

namespace parparchik {

namespace {

std::string RequireEnv(const char* name) {
  const char* value = std::getenv(name);
  if (value == nullptr || value[0] == '\0') {
    throw std::runtime_error(
        std::string("Required environment variable not set: ") + name);
  }
  return value;
}

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
  config.public_bucket = RequireEnv("PARPARCHIK_PUBLIC_BUCKET");
  config.private_bucket = RequireEnv("PARPARCHIK_PRIVATE_BUCKET");
  config.aws_region = GetEnvOr("AWS_REGION", "us-east-1");
  config.s3_endpoint = GetEnvOr("S3_ENDPOINT", "");
  config.registry_manifest_key =
      GetEnvOr("PARPARCHIK_REGISTRY_MANIFEST_KEY", ".parparchik/files.json");
  config.host = GetEnvOr("PARPARCHIK_HOST", "0.0.0.0");

  std::string port_str = GetEnvOr("PARPARCHIK_PORT", "8080");
  config.port = static_cast<uint16_t>(std::stoi(port_str));

  return config;
}

}  // namespace parparchik
