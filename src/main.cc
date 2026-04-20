#include <csignal>
#include <iostream>

#include <aws/core/Aws.h>

#include "parparchik/config.h"
#include "parparchik/server.h"

namespace {
parparchik::Server* g_server = nullptr;

void SignalHandler(int /*signal*/) {
  if (g_server != nullptr) {
    g_server->Stop();
  }
}
}  // namespace

int main() {
  Aws::SDKOptions options;
  Aws::InitAPI(options);

  int exit_code = 0;
  try {
    auto config = parparchik::Config::FromEnv();
    parparchik::Server server(std::move(config));

    g_server = &server;
    std::signal(SIGINT, SignalHandler);
    std::signal(SIGTERM, SignalHandler);

    server.Run();
  } catch (const std::exception& e) {
    std::cerr << "Fatal: " << e.what() << std::endl;
    exit_code = 1;
  }

  Aws::ShutdownAPI(options);
  return exit_code;
}
