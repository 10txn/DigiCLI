BINARY := digicli
CMD    := ./cmd/digicli
DIST   := dist

PREFIX ?= $(HOME)/.local

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

REPO   := 10txn/digicli

.PHONY: all build run test fmt vet tidy clean release install uninstall formula

all: build

build:
	go build -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY) $(CMD)

install:
	@mkdir -p $(PREFIX)/bin
	go build -trimpath -ldflags="$(LDFLAGS)" -o $(PREFIX)/bin/$(BINARY) $(CMD)
	@echo "installed $(PREFIX)/bin/$(BINARY) ($(VERSION))"
	@command -v $(BINARY) >/dev/null 2>&1 || \
		echo "note: $(PREFIX)/bin is not on your PATH — add it to ~/.zshrc"

uninstall:
	rm -f $(PREFIX)/bin/$(BINARY)
	@echo "removed $(PREFIX)/bin/$(BINARY)"

run:
	go run -trimpath $(CMD)

test:
	go test ./...

fmt:
	gofmt -l -w .

vet:
	go vet ./...

tidy:
	go mod tidy

clean:
	rm -rf $(BINARY) $(DIST)

# Cross-platform binaries for GitHub release.
release: clean
	@mkdir -p $(DIST)
	GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags="-s -w $(LDFLAGS)" -o $(DIST)/$(BINARY)-darwin-arm64  $(CMD)
	GOOS=darwin  GOARCH=amd64 go build -trimpath -ldflags="-s -w $(LDFLAGS)" -o $(DIST)/$(BINARY)-darwin-amd64  $(CMD)
	GOOS=linux   GOARCH=arm64 go build -trimpath -ldflags="-s -w $(LDFLAGS)" -o $(DIST)/$(BINARY)-linux-arm64   $(CMD)
	GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags="-s -w $(LDFLAGS)" -o $(DIST)/$(BINARY)-linux-amd64   $(CMD)
	GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w $(LDFLAGS)" -o $(DIST)/$(BINARY)-windows-amd64.exe $(CMD)
	cd $(DIST) && shasum -a 256 * > SHA256SUMS
	@echo "$(DIST)/ built at $(VERSION) — upload these with the GitHub release"

# Point the Homebrew formula at a tag. The tag has to be pushed first: the
# checksum is of GitHub's source tarball for it, which does not exist until
# then. Copy the result into the 10txn/homebrew-digicli tap to publish it.
formula:
	@test -n "$(TAG)" || { echo "usage: make formula TAG=v0.1.0"; exit 1; }
	@url="https://github.com/$(REPO)/archive/refs/tags/$(TAG).tar.gz"; \
	sha=$$(curl -fsSL "$$url" | shasum -a 256 | cut -d' ' -f1) || \
		{ echo "could not fetch $$url — is the tag pushed?"; exit 1; }; \
	sed -e "s|^  url .*|  url \"$$url\"|" \
	    -e "s|^  sha256 .*|  sha256 \"$$sha\"|" \
	    Formula/$(BINARY).rb > Formula/$(BINARY).rb.tmp && \
		mv Formula/$(BINARY).rb.tmp Formula/$(BINARY).rb; \
	echo "Formula/$(BINARY).rb → $(TAG)"; \
	echo "  sha256 $$sha"
