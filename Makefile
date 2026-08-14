GO ?= go
RCLONE_TAG ?= v1.75.0
VERSION ?= alpha
LDFLAGS := -s -w -X github.com/rclone/rclone/fs.VersionSuffix=$(VERSION)-123pan

.PHONY: build alpha test race fuzz vet contract check clean

build:
	$(GO) build -trimpath -tags noselfupdate -ldflags '$(LDFLAGS)' -o bin/rclone-123 ./cmd/rclone-123

alpha:
	./tools/build-alpha.sh

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

check: vet test

clean:
	$(GO) clean
