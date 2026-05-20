.PHONY: build test vet clean dist build-darwin-arm64 build-darwin-amd64 build-linux-amd64 build-linux-arm64

BINARY := salience
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS := -s -w -X 'main.buildVersion=$(VERSION)'
DIST := dist

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -f $(BINARY)
	rm -f salience.db salience.db-wal salience.db-shm
	rm -rf $(DIST)

# Cross-compile targets — each writes a single binary into dist/.

build-darwin-arm64:
	@mkdir -p $(DIST)
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-darwin-arm64 .

build-darwin-amd64:
	@mkdir -p $(DIST)
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-darwin-amd64 .

build-linux-amd64:
	@mkdir -p $(DIST)
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-linux-amd64 .

build-linux-arm64:
	@mkdir -p $(DIST)
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-linux-arm64 .

# Cross-compile every supported (os, arch) pair into dist/.
dist: build-darwin-arm64 build-darwin-amd64 build-linux-amd64 build-linux-arm64
	@echo "wrote:" && ls -1 $(DIST)
