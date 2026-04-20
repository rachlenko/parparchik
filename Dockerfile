FROM ubuntu:24.04 AS builder

RUN apt-get update && apt-get install -y \
    build-essential cmake git curl zip unzip tar pkg-config \
    libcurl4-openssl-dev libssl-dev \
    && rm -rf /var/lib/apt/lists/*

# Install sccache
ARG SCCACHE_VERSION=0.8.2
RUN ARCH="$(uname -m)" && \
    curl -fsSL "https://github.com/mozilla/sccache/releases/download/v${SCCACHE_VERSION}/sccache-v${SCCACHE_VERSION}-${ARCH}-unknown-linux-musl.tar.gz" \
    | tar -xz -C /usr/local/bin --strip-components=1 \
        "sccache-v${SCCACHE_VERSION}-${ARCH}-unknown-linux-musl/sccache"

# Clone vcpkg
RUN git clone https://github.com/microsoft/vcpkg.git /opt/vcpkg && \
    /opt/vcpkg/bootstrap-vcpkg.sh -disableMetrics

ENV VCPKG_ROOT=/opt/vcpkg

WORKDIR /app
COPY vcpkg.json CMakeLists.txt CMakePresets.json ./
COPY include/ include/
COPY src/ src/

RUN cmake -B build \
    -DCMAKE_TOOLCHAIN_FILE=/opt/vcpkg/scripts/buildsystems/vcpkg.cmake \
    -DCMAKE_C_COMPILER_LAUNCHER=sccache \
    -DCMAKE_CXX_COMPILER_LAUNCHER=sccache \
    -DCMAKE_BUILD_TYPE=Release && \
    cmake --build build -j$(nproc)

FROM ubuntu:24.04
RUN apt-get update && apt-get install -y libcurl4 ca-certificates && rm -rf /var/lib/apt/lists/*

COPY --from=builder /app/build/parparchik /usr/local/bin/parparchik

EXPOSE 8080
ENTRYPOINT ["parparchik"]
