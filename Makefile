APP_NAME ?= backup-crunch
BINARY := bin/$(APP_NAME)
PKG := ./cmd/$(APP_NAME)

.PHONY: help build build-windows test vet fmt fmt-check lint clean

default: help

help:
	@echo "$(APP_NAME) — merge scattered backup trees into one best-version tree"
	@echo
	@perl -nle'print $& if m{^[a-zA-Z_-]+:.*?## .*$$}' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

build: ## Build the binary for the current platform (-> bin/backup-crunch)
	go build -o $(BINARY) $(PKG)

build-windows: ## Cross-compile a Windows amd64 binary (-> bin/backup-crunch.exe)
	GOOS=windows GOARCH=amd64 go build -o $(BINARY).exe $(PKG)

test: ## Run all tests with the race detector
	go test -race ./...

vet: ## Run go vet
	go vet ./...

fmt: ## Format all Go code in place
	gofmt -w cmd internal

fmt-check: ## Fail if any Go file is not gofmt-clean
	@out=$$(gofmt -l cmd internal); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

lint: fmt-check vet ## Run fmt-check and vet

clean: ## Remove build artifacts
	rm -rf bin
