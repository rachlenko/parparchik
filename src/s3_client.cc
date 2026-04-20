#include "parparchik/s3_client.h"

#include <sstream>
#include <stdexcept>

#include <aws/core/Aws.h>
#include <aws/core/auth/AWSCredentialsProvider.h>
#include <aws/s3/model/CopyObjectRequest.h>
#include <aws/s3/model/CreateBucketRequest.h>
#include <aws/s3/model/DeleteObjectRequest.h>
#include <aws/s3/model/GetObjectRequest.h>
#include <aws/s3/model/HeadObjectRequest.h>
#include <aws/s3/model/ListObjectsV2Request.h>

namespace parparchik {

S3Client::S3Client(const std::string& region, const std::string& endpoint)
    : endpoint_(endpoint), region_(region) {
  Aws::Client::ClientConfiguration client_config;
  client_config.region = region;

  if (!endpoint.empty()) {
    client_config.endpointOverride = endpoint;
    client_config.scheme = Aws::Http::Scheme::HTTP;
    client_config.verifySSL = false;
    client_ = Aws::S3::S3Client(
        client_config,
        Aws::Client::AWSAuthV4Signer::PayloadSigningPolicy::Never,
        /*useVirtualAddressing=*/false);
  } else {
    client_ = Aws::S3::S3Client(client_config);
  }
}

std::vector<S3Object> S3Client::ListObjects(
    const std::string& bucket) const {
  std::vector<S3Object> result;

  Aws::S3::Model::ListObjectsV2Request request;
  request.SetBucket(bucket);

  bool has_more = true;
  while (has_more) {
    auto outcome = client_.ListObjectsV2(request);
    if (!outcome.IsSuccess()) {
      throw std::runtime_error("Failed to list objects in bucket " + bucket +
                               ": " +
                               outcome.GetError().GetMessage());
    }

    const auto& contents = outcome.GetResult().GetContents();
    for (const auto& object : contents) {
      result.push_back({
          .key = object.GetKey(),
          .size = object.GetSize(),
          .last_modified = object.GetLastModified().ToGmtString(
              Aws::Utils::DateFormat::ISO_8601),
      });
    }

    has_more = outcome.GetResult().GetIsTruncated();
    if (has_more) {
      request.SetContinuationToken(
          outcome.GetResult().GetNextContinuationToken());
    }
  }

  return result;
}

bool S3Client::ObjectExists(const std::string& bucket,
                            const std::string& key) const {
  Aws::S3::Model::HeadObjectRequest request;
  request.SetBucket(bucket);
  request.SetKey(key);

  auto outcome = client_.HeadObject(request);
  return outcome.IsSuccess();
}

std::string S3Client::GetObjectContent(const std::string& bucket,
                                       const std::string& key) const {
  Aws::S3::Model::GetObjectRequest request;
  request.SetBucket(bucket);
  request.SetKey(key);

  auto outcome = client_.GetObject(request);
  if (!outcome.IsSuccess()) {
    throw std::runtime_error("Failed to get object " + key + " from " +
                             bucket + ": " +
                             outcome.GetError().GetMessage());
  }

  std::ostringstream oss;
  oss << outcome.GetResult().GetBody().rdbuf();
  return oss.str();
}

bool S3Client::CopyObject(const std::string& src_bucket,
                           const std::string& src_key,
                           const std::string& dst_bucket,
                           const std::string& dst_key) const {
  Aws::S3::Model::CopyObjectRequest request;
  request.SetBucket(dst_bucket);
  request.SetKey(dst_key);
  request.SetCopySource(src_bucket + "/" + src_key);

  auto outcome = client_.CopyObject(request);
  return outcome.IsSuccess();
}

bool S3Client::DeleteObject(const std::string& bucket,
                            const std::string& key) const {
  Aws::S3::Model::DeleteObjectRequest request;
  request.SetBucket(bucket);
  request.SetKey(key);

  auto outcome = client_.DeleteObject(request);
  return outcome.IsSuccess();
}

std::string S3Client::GetPublicUrl(const std::string& bucket,
                                   const std::string& key) const {
  if (!endpoint_.empty()) {
    return "http://" + endpoint_ + "/" + bucket + "/" + key;
  }
  return "https://" + bucket + ".s3." + region_ + ".amazonaws.com/" + key;
}

std::string S3Client::GeneratePresignedUrl(const std::string& bucket,
                                           const std::string& key,
                                           uint64_t expiry_seconds) {
  return client_.GeneratePresignedUrl(bucket, key,
                                      Aws::Http::HttpMethod::HTTP_GET,
                                      expiry_seconds);
}

bool S3Client::CreateBucket(const std::string& bucket) const {
  Aws::S3::Model::CreateBucketRequest request;
  request.SetBucket(bucket);

  auto outcome = client_.CreateBucket(request);
  return outcome.IsSuccess();
}

}  // namespace parparchik
