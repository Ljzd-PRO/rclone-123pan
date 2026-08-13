GO ?= go
RCLONE_TAG ?= v1.75.0
VERSION ?= alpha
LDFLAGS := -s -w -X github.com/rclone/rclone/fs.VersionSuffix=$(VERSION)-123pan

.PHONY: build test race vet check clean

build:
	$(GO) build -trimpath -tags noselfupdate -ldflags '$(LDFLAGS)' -o bin/rclone-123 ./cmd/rclone-123

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

check: vet test

clean:
	$(GO) clean

