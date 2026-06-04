ifeq ($(OS),Windows_NT)
BIN ?= bin/whale.exe
else
BIN ?= bin/whale
endif
GOCACHE_DIR ?= $(CURDIR)/.gocache
VERSION ?= dev
LDFLAGS := -X github.com/usewhale/whale/internal/build.Version=$(VERSION)

.PHONY: help build fmt-check vet test test-tui test-evals test-windows bench-cost bench-cost-live run clean

export BIN
export GOCACHE_DIR
export VERSION
export LDFLAGS

help:
	@go run ./cmd/dev help

build:
	@go run ./cmd/dev build

fmt-check:
	@go run ./cmd/dev fmt-check

vet:
	@go run ./cmd/dev vet

test:
	@go run ./cmd/dev test

test-evals:
	@go run ./cmd/dev test-evals

test-tui:
	@go run ./cmd/dev test-tui

test-windows:
	@go run ./cmd/dev test-windows

bench-cost:
	@scripts/bench/cost.sh

bench-cost-live:
	@scripts/bench/cost.sh --live

run:
	@go run ./cmd/dev run

clean:
	@go run ./cmd/dev clean
