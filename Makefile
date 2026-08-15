GO ?= go
VERSION_SUFFIX := $(shell ./tools/version.sh suffix)
LDFLAGS := -s -w -X github.com/rclone/rclone/fs.VersionSuffix=$(VERSION_SUFFIX)

.PHONY: build alpha test-version test-deb test race fuzz vet contract check clean

build:
	$(GO) build -buildvcs=false -trimpath -tags noselfupdate -ldflags '$(LDFLAGS)' -o bin/rclone ./cmd/rclone

alpha:
	./tools/build-alpha.sh

test-version:
	./tools/test-version.sh

test-deb:
	./tools/test-deb-packages.sh dist

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

fuzz:
	$(GO) test ./backend/123pan -run '^$$' -fuzz '^FuzzDecodeEnvelope$$' -fuzztime=5s
	$(GO) test ./backend/123pan -run '^$$' -fuzz '^FuzzUploadPartCount$$' -fuzztime=5s

vet:
	$(GO) vet ./...

contract:
	./tools/test-rclone-contract.sh

check: test-version vet test

clean:
	$(GO) clean
