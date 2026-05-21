BINARY   := luckfox-config
CMD      := ./cmd/luckfox-config
DIST     := dist

# ARM target for Luckfox Lyra (Cortex-A7, RK3506)
GOARCH   ?= arm
GOARM    ?= 7
GOOS     ?= linux

VERSION  ?= v0.0.1
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE     ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS  := -ldflags="-s -w -X luckfox-config/internal/version.Version=$(VERSION) -X luckfox-config/internal/version.Commit=$(COMMIT) -X luckfox-config/internal/version.BuildDate=$(DATE)"

.PHONY: build build-arm build-arm64 build-all build-native clean run-show

## build: cross-compile for ARM Linux (default: GOARCH=arm GOARM=7)
build:
	@mkdir -p $(DIST)
	GOOS=$(GOOS) GOARCH=$(GOARCH) GOARM=$(GOARM) \
	  go build $(LDFLAGS) -o $(DIST)/$(BINARY) $(CMD)
	@echo "Built $(DIST)/$(BINARY) for $(GOOS)/$(GOARCH)v$(GOARM)"

## build-arm: cross-compile for ARM Linux (RK3506)
build-arm:
	@mkdir -p $(DIST)
	GOOS=linux GOARCH=arm GOARM=7 \
	  go build $(LDFLAGS) -o $(DIST)/$(BINARY)-arm $(CMD)
	@echo "Built $(DIST)/$(BINARY)-arm for linux/armv7"

## build-arm64: cross-compile for ARM64 Linux (AArch64)
build-arm64:
	@mkdir -p $(DIST)
	GOOS=linux GOARCH=arm64 \
	  go build $(LDFLAGS) -o $(DIST)/$(BINARY)-arm64 $(CMD)
	@echo "Built $(DIST)/$(BINARY)-arm64 for linux/arm64"

## build-all: build for both ARM and ARM64
build-all: build-arm build-arm64

## build-native: compile for the host machine (useful for TUI testing)
build-native:
	@mkdir -p $(DIST)
	go build $(LDFLAGS) -o $(DIST)/$(BINARY)-host $(CMD)
	@echo "Built $(DIST)/$(BINARY)-host"

## clean: remove build artifacts
clean:
	rm -rf $(DIST)

## help: show this help
help:
	@grep -E '^##' Makefile | sed 's/## //'
