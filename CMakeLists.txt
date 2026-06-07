cmake_minimum_required(VERSION 3.25)
project(parparchik VERSION 0.1.0 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 23)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
set(CMAKE_EXPORT_COMPILE_COMMANDS ON)

# macOS + Apple Clang only: CommandLineTools on macOS 26+ hides libc++ headers.
# Do NOT apply this to Homebrew LLVM / GCC — they ship their own libc++ and
# force-injecting Apple's headers here would mix incompatible standard libraries.
if(APPLE AND CMAKE_CXX_COMPILER_ID STREQUAL "AppleClang")
    execute_process(
        COMMAND xcrun --show-sdk-path
        OUTPUT_VARIABLE _SDK_PATH
        OUTPUT_STRIP_TRAILING_WHITESPACE
    )
    set(_CXX_INCLUDE "${_SDK_PATH}/usr/include/c++/v1")
    if(EXISTS "${_CXX_INCLUDE}")
        add_compile_options(-isystem "${_CXX_INCLUDE}")
    endif()
endif()

# vcpkg packages (via toolchain file)
find_package(httplib CONFIG REQUIRED)
find_package(nlohmann_json CONFIG REQUIRED)
find_package(prometheus-cpp CONFIG REQUIRED)
find_package(AWSSDK CONFIG REQUIRED COMPONENTS s3)

# Main library
add_library(parparchik_lib STATIC
    src/config.cc
    src/s3_client.cc
    src/file_registry.cc
    src/metrics.cc
    src/server.cc
)

target_include_directories(parparchik_lib PUBLIC include)

target_link_libraries(parparchik_lib
    PUBLIC
        httplib::httplib
        nlohmann_json::nlohmann_json
        prometheus-cpp::core
        ${AWSSDK_LINK_LIBRARIES}
)

# Executable
add_executable(parparchik src/main.cc)
target_link_libraries(parparchik PRIVATE parparchik_lib)
