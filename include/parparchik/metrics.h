#ifndef PARPARCHIK_METRICS_H_
#define PARPARCHIK_METRICS_H_

#include <memory>
#include <string>
#include <vector>

#include <prometheus/gauge.h>
#include <prometheus/registry.h>

#include "parparchik/file_registry.h"

namespace parparchik {

class Metrics {
 public:
  Metrics();

  void ObserveFiles(const std::vector<FileEntry>& entries);
  std::string Render() const;

 private:
  std::shared_ptr<prometheus::Registry> registry_;
  prometheus::Gauge& volume_files_public_;
  prometheus::Gauge& volume_files_private_;
  prometheus::Gauge& uploads_per_week_;
  prometheus::Gauge& uploads_per_month_;
};

}  // namespace parparchik

#endif  // PARPARCHIK_METRICS_H_
