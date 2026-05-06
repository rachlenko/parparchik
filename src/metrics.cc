#include "parparchik/metrics.h"

#include <chrono>
#include <ctime>
#include <iomanip>
#include <sstream>

#include <prometheus/text_serializer.h>

namespace parparchik {

namespace {

std::chrono::system_clock::time_point ParseIso8601(
    const std::string& timestamp) {
  if (timestamp.size() < 19) {
    return std::chrono::system_clock::time_point{};
  }

  std::tm tm{};
  std::istringstream stream(timestamp.substr(0, 19));
  stream >> std::get_time(&tm, "%Y-%m-%dT%H:%M:%S");
  if (stream.fail()) {
    return std::chrono::system_clock::time_point{};
  }

#if defined(_WIN32)
  const std::time_t time = _mkgmtime(&tm);
#else
  const std::time_t time = timegm(&tm);
#endif
  if (time == static_cast<std::time_t>(-1)) {
    return std::chrono::system_clock::time_point{};
  }

  return std::chrono::system_clock::from_time_t(time);
}

bool IsWithin(const std::string& timestamp,
              std::chrono::system_clock::duration window) {
  const auto uploaded_at = ParseIso8601(timestamp);
  if (uploaded_at == std::chrono::system_clock::time_point{}) {
    return false;
  }

  const auto now = std::chrono::system_clock::now();
  return uploaded_at <= now && uploaded_at >= now - window;
}

}  // namespace

Metrics::Metrics()
    : registry_(std::make_shared<prometheus::Registry>()),
      volume_files_family_(
          prometheus::BuildGauge()
              .Name("parparchik_volume_files")
              .Help("Current number of known files by bucket.")
              .Register(*registry_)),
      uploads_per_week_(
          prometheus::BuildGauge()
              .Name("parparchik_uploads_per_week")
              .Help(
                  "Known uploaded file versions modified during the last 7 "
                  "days.")
              .Register(*registry_)
              .Add({})),
      uploads_per_month_(
          prometheus::BuildGauge()
              .Name("parparchik_uploads_per_month")
              .Help(
                  "Known uploaded file versions modified during the last 31 "
                  "days.")
              .Register(*registry_)
              .Add({})) {}

void Metrics::ObserveFiles(const std::vector<FileEntry>& entries) {
  std::unordered_map<std::string, int> counts;
  int week = 0;
  int month = 0;

  for (const auto& entry : entries) {
    ++counts[entry.bucket_name];
    if (IsWithin(entry.last_modified, std::chrono::hours(24 * 7))) {
      ++week;
    }
    if (IsWithin(entry.last_modified, std::chrono::hours(24 * 31))) {
      ++month;
    }
  }

  for (auto& [bucket, gauge] : volume_files_gauges_) {
    gauge->Set(0);
  }
  for (const auto& [bucket, count] : counts) {
    auto it = volume_files_gauges_.find(bucket);
    if (it == volume_files_gauges_.end()) {
      auto& gauge = volume_files_family_.Add({{"volume", bucket}});
      volume_files_gauges_[bucket] = &gauge;
      gauge.Set(count);
    } else {
      it->second->Set(count);
    }
  }

  uploads_per_week_.Set(week);
  uploads_per_month_.Set(month);
}

std::string Metrics::Render() const {
  std::ostringstream output;
  prometheus::TextSerializer serializer;
  serializer.Serialize(output, registry_->Collect());
  return output.str();
}

}  // namespace parparchik
