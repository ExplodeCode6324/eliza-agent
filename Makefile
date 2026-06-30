# ELIZA Agent Makefile

BINARY=eliza
LDFLAGS=-ldflags="-s -w"

.PHONY: build build-all build-windows clean

build:
	CGO_ENABLED=0 go build $(LDFLAGS) -o $(BINARY) ./cmd/eliza/

build-all:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY)- ./cmd/eliza/linux-amd64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY)- ./cmd/eliza/linux-arm64 .
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY)- ./cmd/eliza/windows-amd64.exe .

build-windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY)- ./cmd/eliza/windows-amd64.exe .

clean:
	rm -f $(BINARY) $(BINARY)-linux-amd64 $(BINARY)-linux-arm64 $(BINARY)-windows-amd64.exe

test:
	go vet ./cmd/eliza/
