# syntax=docker/dockerfile:1
# tgtui, fully static Linux binary: Go + TDLib (musl, static OpenSSL/zlib/libstdc++).
#   docker build --output . .        # writes ./tgtui (make build)
# TDLib compile units take ~1.5 GB RAM each; raise BUILD_JOBS only with RAM to spare.
# Release builds embed the API credentials from the secrets api_id / api_hash
# (docker build --secret id=api_id,env=TGTUI_API_ID …); without them the binary asks
# for the user's own.
FROM golang:1.26-alpine AS build
RUN apk add --no-cache build-base linux-headers git cmake gperf perl \
    zlib-dev zlib-static openssl-dev openssl-libs-static pkgconf
ARG TDLIB_COMMIT=a94b83d67ab4c8397c6245e541b31a4131ea2f79
ARG BUILD_JOBS=3
WORKDIR /td
RUN git init -q && git remote add origin https://github.com/tdlib/td.git \
 && git fetch -q --depth 1 origin $TDLIB_COMMIT && git checkout -q FETCH_HEAD
RUN cmake -S . -B build -DCMAKE_BUILD_TYPE=Release -DCMAKE_INSTALL_PREFIX=/usr/local \
      -DOPENSSL_USE_STATIC_LIBS=TRUE -DZLIB_USE_STATIC_LIBS=TRUE \
 && cmake --build build --target install -j $BUILD_JOBS && rm -rf build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=secret,id=api_id,env=API_ID --mount=type=secret,id=api_hash,env=API_HASH \
    CGO_ENABLED=1 CGO_LDFLAGS="$(pkg-config --static --libs tdjson_static) -lstdc++" \
    go build -ldflags "-s -w -X main.version=$VERSION -X main.apiID=$API_ID -X main.apiHash=$API_HASH \
      -linkmode external -extldflags -static" -o /tgtui ./cmd/tgtui

FROM scratch
COPY --from=build /tgtui /
