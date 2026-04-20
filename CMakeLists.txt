cmake_minimum_required(VERSION 3.25)
project(parparchik VERSION 0.1.0 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
set(CMAKE_EXPORT_COMPILE_COMMANDS ON)

# vcpkg packages (via toolchain file)
find_package(httplib CONFIG REQUIRED)
find_package(nlohmann_json CONFIG REQUIRED)
find_package(AWSSDK CONFIG REQUIRED COMPONENTS s3)

# Main library
add_library(parparchik_lib STATIC
    src/config.cc
    src/s3_client.cc
    src/file_registry.cc
    src/server.cc
)

target_include_directories(parparchik_lib PUBLIC include)

target_link_libraries(parparchik_lib
    PUBLIC
        httplib::httplib
        nlohmann_json::nlohmann_json
        ${AWSSDK_LINK_LIBRARIES}
)

# Executable
add_executable(parparchik src/main.cc)
target_link_libraries(parparchik PRIVATE parparchik_lib)
