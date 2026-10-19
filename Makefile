VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
# Everything except the cgo parts, which need TDLib and build only in the container.
PURE = $(shell go list ./... | grep -v -e /internal/td -e /cmd/ -e /spike)

# Static binary built in Docker (TDLib lives only there); runs on any Linux.
build:
	docker build --build-arg VERSION=$(VERSION) --output . .

test:
	go vet $(PURE) && go test $(PURE)

# Loads TGTUI_API_ID / TGTUI_API_HASH from .env. Pass flags with ARGS=--test-dc.
run: build
	set -a; [ ! -f .env ] || . ./.env; set +a; ./tgtui $(ARGS)

.PHONY: build test run
