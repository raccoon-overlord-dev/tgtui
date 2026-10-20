VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
# Everything except the cgo parts, which need TDLib and build only in the container.
PURE = $(shell go list ./... | grep -v -e /internal/td -e /cmd/ -e /spike)
TDLIB_COMMIT = $(shell sed -n 's/^ARG TDLIB_COMMIT=//p' Dockerfile)
BUILD_JOBS ?= 3

ifeq ($(shell uname -s),Darwin)
# Native macOS build. Needs the Xcode command line tools and
#   brew install go cmake gperf openssl@3 pkgconf
# TDLib compiles once into TDLIB; OpenSSL is linked statically, so the binary depends only
# on system libraries. Release builds pass API_ID / API_HASH.
TDLIB ?= $(HOME)/.cache/tgtui-build/tdlib-$(TDLIB_COMMIT)
OPENSSL = $(shell brew --prefix openssl@3)
# ponytail: Homebrew's OpenSSL targets the build machine's macOS (the linker warns);
# build OpenSSL from source with this target if it fails on older macOS.
export MACOSX_DEPLOYMENT_TARGET ?= 12.0

build: $(TDLIB)/lib/libtdjson_static.a
	CGO_ENABLED=1 CGO_CFLAGS="-I$(TDLIB)/include" \
	CGO_LDFLAGS="$$(PKG_CONFIG_PATH=$(TDLIB)/lib/pkgconfig pkg-config --static --libs tdjson_static \
	  | sed 's|-lssl|$(OPENSSL)/lib/libssl.a|g; s|-lcrypto|$(OPENSSL)/lib/libcrypto.a|g') -lc++" \
	go build -ldflags "-s -w -X main.version=$(VERSION) -X main.apiID=$(API_ID) -X main.apiHash=$(API_HASH)" \
	  -o tgtui ./cmd/tgtui

# TDLib's pkg-config file names OpenSSL as -lssl / -lcrypto, which macOS would resolve to
# the dylibs, hence the sed above.
$(TDLIB)/lib/libtdjson_static.a:
	rm -rf $(TDLIB).src && git init -q $(TDLIB).src
	git -C $(TDLIB).src fetch -q --depth 1 https://github.com/tdlib/td.git $(TDLIB_COMMIT)
	git -C $(TDLIB).src checkout -q FETCH_HEAD
	cmake -S $(TDLIB).src -B $(TDLIB).src/build -DCMAKE_BUILD_TYPE=Release -DCMAKE_INSTALL_PREFIX=$(TDLIB) \
	  -DOPENSSL_ROOT_DIR=$(OPENSSL) -DOPENSSL_USE_STATIC_LIBS=TRUE
	cmake --build $(TDLIB).src/build --target install -j $(BUILD_JOBS)
	rm -rf $(TDLIB).src
else
# Static binary built in Docker (TDLib lives only there); runs on any Linux.
build:
	docker build --build-arg VERSION=$(VERSION) --build-arg BUILD_JOBS=$(BUILD_JOBS) --output . .
endif

test:
	go vet $(PURE) && go test $(PURE)

# Loads TGTUI_API_ID / TGTUI_API_HASH from .env. Pass flags with ARGS=--test-dc.
run: build
	set -a; [ ! -f .env ] || . ./.env; set +a; ./tgtui $(ARGS)

.PHONY: build test run
