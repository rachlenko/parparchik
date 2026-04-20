#ifndef PARPARCHIK_SERVER_H_
#define PARPARCHIK_SERVER_H_

#include <memory>

#include <httplib.h>

#include "parparchik/config.h"
#include "parparchik/file_registry.h"
#include "parparchik/s3_client.h"

namespace parparchik {

class Server {
 public:
  explicit Server(Config config);

  void Run();
  void Stop();

 private:
  void RegisterRoutes();
  void SyncRegistry();

  void HandleStatus(const httplib::Request& req, httplib::Response& res);
  void HandleList(const httplib::Request& req, httplib::Response& res);
  void HandleUpdate(const httplib::Request& req, httplib::Response& res);
  void HandleFileDownload(const httplib::Request& req, httplib::Response& res);

  Config config_;
  httplib::Server http_;
  std::unique_ptr<S3Client> s3_;
  std::unique_ptr<FileRegistry> registry_;
};

}  // namespace parparchik

#endif  // PARPARCHIK_SERVER_H_
