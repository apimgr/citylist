# CityList API - Simplified Makefile
PROJECTNAME = citylist
PROJECTORG = apimgr

# Version management: Use VERSION env var, or read from release.txt, or default to 0.0.1
VERSION ?= $(shell cat release.txt 2>/dev/null || echo "0.0.1")
COMMIT = $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE = $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS = -ldflags "-X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.BuildDate=$(BUILD_DATE) -w -s"

# Platforms: OS-ARCH pairs
PLATFORMS = \
	linux-amd64 \
	linux-arm64 \
	windows-amd64 \
	windows-arm64 \
	darwin-amd64 \
	darwin-arm64 \
	freebsd-amd64 \
	freebsd-arm64

.PHONY: build releases docker test clean help version-bump

help:
	@echo "CityList API - Makefile"
	@echo ""
	@echo "Targets:"
	@echo "  build        - Build for all platforms + host binary"
	@echo "  releases      - Increment version and create GitHub releases artifacts"
	@echo "  docker       - Build and push Docker image to ghcr.io"
	@echo "  test         - Run all tests"
	@echo "  clean        - Remove build artifacts"
	@echo ""
	@echo "Current version: $(VERSION)"
	@echo "Set VERSION env var to override: make build VERSION=1.0.0"

# Build for all platforms + host binary
build:
	@echo "Building $(PROJECTNAME) $(VERSION) for all platforms..."
	@mkdir -p binaries
	@echo "$(VERSION)" > release.txt
	@docker run --rm -v $$(pwd):/workspace -w /workspace golang:alpine sh -c ' \
		apk add --no-cache git make binutils >/dev/null 2>&1 && \
		go mod download && \
		echo "→ Linux AMD64" && \
		GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o binaries/$(PROJECTNAME)-linux-amd64 ./src && \
		strip binaries/$(PROJECTNAME)-linux-amd64 2>/dev/null || true && \
		echo "→ Linux ARM64" && \
		GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -o binaries/$(PROJECTNAME)-linux-arm64 ./src && \
		strip binaries/$(PROJECTNAME)-linux-arm64 2>/dev/null || true && \
		echo "→ Windows AMD64" && \
		GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o binaries/$(PROJECTNAME)-windows-amd64.exe ./src && \
		echo "→ Windows ARM64" && \
		GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -o binaries/$(PROJECTNAME)-windows-arm64.exe ./src && \
		echo "→ macOS AMD64" && \
		GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o binaries/$(PROJECTNAME)-darwin-amd64 ./src && \
		echo "→ macOS ARM64" && \
		GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -o binaries/$(PROJECTNAME)-darwin-arm64 ./src && \
		echo "→ FreeBSD AMD64" && \
		GOOS=freebsd GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o binaries/$(PROJECTNAME)-freebsd-amd64 ./src && \
		echo "→ FreeBSD ARM64" && \
		GOOS=freebsd GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -o binaries/$(PROJECTNAME)-freebsd-arm64 ./src && \
		echo "→ Host Binary" && \
		CGO_ENABLED=0 go build $(LDFLAGS) -o binaries/$(PROJECTNAME) ./src \
	'
	@echo "✅ Build complete: $(VERSION)"
	@echo ""
	@ls -lh binaries/ | grep $(PROJECTNAME)

# Create GitHub releases (auto-increment version, delete tag if exists)
release:
	@echo "Preparing GitHub releases for $(PROJECTNAME)..."
	@mkdir -p releases
	@# Read current version or default
	@CURRENT_VERSION=$$(cat release.txt 2>/dev/null || echo "0.0.1"); \
	if [ -n "$$VERSION" ]; then \
		NEW_VERSION=$$VERSION; \
	else \
		MAJOR=$$(echo $$CURRENT_VERSION | cut -d. -f1); \
		MINOR=$$(echo $$CURRENT_VERSION | cut -d. -f2); \
		PATCH=$$(echo $$CURRENT_VERSION | cut -d. -f3); \
		PATCH=$$((PATCH + 1)); \
		NEW_VERSION="$$MAJOR.$$MINOR.$$PATCH"; \
	fi; \
	echo "Version: $$CURRENT_VERSION → $$NEW_VERSION"; \
	echo "$$NEW_VERSION" > release.txt; \
	NEW_VERSION_VAR=$$NEW_VERSION $(MAKE) build VERSION=$$NEW_VERSION; \
	cp binaries/$(PROJECTNAME)-* releases/ 2>/dev/null || true; \
	cd releases && sha256sum $(PROJECTNAME)-* > SHA256SUMS 2>/dev/null || true; \
	echo "" > releases/RELEASE_NOTES.md; \
	echo "## $(PROJECTNAME) v$$NEW_VERSION" >> releases/RELEASE_NOTES.md; \
	echo "" >> releases/RELEASE_NOTES.md; \
	echo "**Build Date:** $(BUILD_DATE)" >> releases/RELEASE_NOTES.md; \
	echo "**Commit:** $(COMMIT)" >> releases/RELEASE_NOTES.md; \
	echo "" >> releases/RELEASE_NOTES.md; \
	echo "### Downloads" >> releases/RELEASE_NOTES.md; \
	echo "" >> releases/RELEASE_NOTES.md; \
	echo "| Platform | Architecture | Binary |" >> releases/RELEASE_NOTES.md; \
	echo "|----------|--------------|--------|" >> releases/RELEASE_NOTES.md; \
	echo "| Linux | x86_64 | \`$(PROJECTNAME)-linux-amd64\` |" >> releases/RELEASE_NOTES.md; \
	echo "| Linux | ARM64 | \`$(PROJECTNAME)-linux-arm64\` |" >> releases/RELEASE_NOTES.md; \
	echo "| Windows | x86_64 | \`$(PROJECTNAME)-windows-amd64.exe\` |" >> releases/RELEASE_NOTES.md; \
	echo "| Windows | ARM64 | \`$(PROJECTNAME)-windows-arm64.exe\` |" >> releases/RELEASE_NOTES.md; \
	echo "| macOS | Intel | \`$(PROJECTNAME)-darwin-amd64\` |" >> releases/RELEASE_NOTES.md; \
	echo "| macOS | Apple Silicon | \`$(PROJECTNAME)-darwin-arm64\` |" >> releases/RELEASE_NOTES.md; \
	echo "| FreeBSD | x86_64 | \`$(PROJECTNAME)-freebsd-amd64\` |" >> releases/RELEASE_NOTES.md; \
	echo "| FreeBSD | ARM64 | \`$(PROJECTNAME)-freebsd-arm64\` |" >> releases/RELEASE_NOTES.md; \
	echo "" >> releases/RELEASE_NOTES.md; \
	echo "### Verification" >> releases/RELEASE_NOTES.md; \
	echo "" >> releases/RELEASE_NOTES.md; \
	echo "SHA256 checksums are provided in \`SHA256SUMS\`" >> releases/RELEASE_NOTES.md; \
	echo "" >> releases/RELEASE_NOTES.md; \
	echo "✅ Release v$$NEW_VERSION ready in releases/"; \
	echo ""; \
	echo "Deleting existing tag v$$NEW_VERSION if it exists..."; \
	gh releases delete v$$NEW_VERSION -y 2>/dev/null || true; \
	git tag -d v$$NEW_VERSION 2>/dev/null || true; \
	git push origin :refs/tags/v$$NEW_VERSION 2>/dev/null || true; \
	echo "Creating GitHub releases v$$NEW_VERSION..."; \
	@mkdir -p releases
	@echo "Copying platform binaries to releases..."
	@cp binaries/$(PROJECTNAME)-* releases/ 2>/dev/null || { echo "Error: Build first"; exit 1; }
	@echo "Creating source archives (no VCS files)..."
	@git archive --format=tar.gz --prefix=$(PROJECTNAME)-$(VERSION)/ HEAD -o releases/$(PROJECTNAME)-$(VERSION)-src.tar.gz
	@git archive --format=zip --prefix=$(PROJECTNAME)-$(VERSION)/ HEAD -o releases/$(PROJECTNAME)-$(VERSION)-src.zip
	gh releases create v$$NEW_VERSION releases/* \
		--title "v$$NEW_VERSION" \
		--notes-file releases/RELEASE_NOTES.md; \
	echo ""; \
	echo "✅ GitHub releases v$$NEW_VERSION created successfully"; \
	echo "   https://github.com/$(PROJECTORG)/$(PROJECTNAME)/releases/tag/v$$NEW_VERSION"

# Build and push multi-arch Docker image to ghcr.io
docker:
	@echo "Building multi-arch Docker image for $(PROJECTNAME) $(VERSION)..."
	@echo "$(VERSION)" > release.txt
	@# Ensure buildx is available
	@docker buildx version >/dev/null 2>&1 || (echo "❌ Docker buildx not found. Install it first." && exit 1)
	@# Create builder if it doesn't exist
	@docker buildx create --name $(PROJECTNAME)-builder --use 2>/dev/null || docker buildx use $(PROJECTNAME)-builder 2>/dev/null || true
	@# Build and push multi-arch image for both amd64 and arm64
	@echo "Building for linux/amd64,linux/arm64..."
	@docker buildx build \
		--platform linux/amd64,linux/arm64 \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t ghcr.io/$(PROJECTORG)/$(PROJECTNAME):$(VERSION) \
		-t ghcr.io/$(PROJECTORG)/$(PROJECTNAME):latest \
		--push \
		.
	@echo ""
	@echo "✅ Multi-arch Docker image pushed successfully"
	@echo "   📦 ghcr.io/$(PROJECTORG)/$(PROJECTNAME):$(VERSION)"
	@echo "   📦 ghcr.io/$(PROJECTORG)/$(PROJECTNAME):latest"
	@echo ""
	@echo "📊 Platforms: linux/amd64, linux/arm64"
	@echo "🏷️  Tags: $(VERSION), latest"

# Build development Docker image (local only, not pushed)
docker-dev:
	@echo "Building development Docker image for $(PROJECTNAME)..."
	@docker build \
		--build-arg VERSION=dev \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t $(PROJECTNAME):dev \
		.
	@echo ""
	@echo "✅ Development Docker image built successfully"
	@echo "   📦 $(PROJECTNAME):dev"
	@echo ""
	@echo "Run with: docker run -p 8080:80 $(PROJECTNAME):dev"

# Run all tests
test:
	@echo "Running tests for $(PROJECTNAME)..."
	@go test -v -race -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | grep total:
	@echo "✅ Tests complete"

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf binaries/ releases/ coverage.out
	@go clean
	@echo "✅ Clean complete"

# Version bump helper (for manual semantic versioning)
version-bump:
	@echo "Current version: $(VERSION)"
	@echo "Usage:"
	@echo "  make releases VERSION=1.0.0    # Set specific version"
	@echo "  make releases                  # Auto-increment patch (x.x.PATCH+1)"

.DEFAULT_GOAL := help
