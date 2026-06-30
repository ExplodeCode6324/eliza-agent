# ELIZA Agent Makefile

BINARY=eliza
DIST_DIR=binaries
LDFLAGS=-ldflags="-s -w"

.PHONY: build build-all build-windows clean test

build:
	mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 go build $(LDFLAGS) -o $(DIST_DIR)/$(BINARY)-$(shell go env GOOS)-$(shell go env GOARCH) ./cmd/eliza/

build-all:
	mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(DIST_DIR)/$(BINARY)-linux-amd64 ./cmd/eliza/
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(DIST_DIR)/$(BINARY)-linux-arm64 ./cmd/eliza/
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(DIST_DIR)/$(BINARY)-darwin-amd64 ./cmd/eliza/
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(DIST_DIR)/$(BINARY)-darwin-arm64 ./cmd/eliza/
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(DIST_DIR)/$(BINARY)-windows-amd64.exe ./cmd/eliza/

build-windows:
	mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(DIST_DIR)/$(BINARY)-windows-amd64.exe ./cmd/eliza/

clean:
	rm -f $(BINARY) $(BINARY)-linux-amd64 $(BINARY)-linux-arm64 $(BINARY)-windows-amd64.exe
	rm -f $(DIST_DIR)/$(BINARY)-linux-amd64 $(DIST_DIR)/$(BINARY)-linux-arm64
	rm -f $(DIST_DIR)/$(BINARY)-darwin-amd64 $(DIST_DIR)/$(BINARY)-darwin-arm64
	rm -f $(DIST_DIR)/$(BINARY)-windows-amd64.exe

test:
	go vet ./cmd/eliza/
