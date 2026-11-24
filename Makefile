# Infer project identity from the configured Git remote, with path fallbacks.
REMOTE_URL := $(shell git remote get-url origin 2>/dev/null)
PROJECT_NAME := $(shell if [ -n "$(REMOTE_URL)" ]; then basename "$(REMOTE_URL)" .git; else basename "$(CURDIR)"; fi)
PROJECT_ORG := $(shell if [ -n "$(REMOTE_URL)" ]; then basename "$$(dirname "$(REMOTE_URL)")" .git; else basename "$$(dirname "$(CURDIR)")"; fi)
INTERNAL_NAME := citylist

# A user VERSION override takes precedence over a non-empty release.txt and the git-derived devel version.
VERSION ?= $(shell v=; [ -r release.txt ] && v=$$(tr -d '[:space:]' < release.txt); if [ -n "$$v" ]; then echo "$$v"; else git rev-parse --short=7 HEAD 2>/dev/null || echo devel; fi)
BUILD_EPOCH := $(shell date -u +%s)
BUILD_DATE := $(shell date -u -d @$(BUILD_EPOCH) +"%Y-%m-%dT%H:%M:%SZ")
COMMIT_ID := $(shell git rev-parse --short=7 HEAD 2>/dev/null || echo N/A)
OFFICIAL_SITE := $(shell if [ -f site.txt ]; then cat site.txt; else printf '%s' "$${OFFICIAL_SITE:-}"; fi)
LDFLAGS := -s -w -X main.Version=$(VERSION) -X main.CommitID=$(COMMIT_ID) -X main.BuildEpoch=$(BUILD_EPOCH) -X main.OfficialSite=$(OFFICIAL_SITE) -X main.internalName=$(INTERNAL_NAME)

BINDIR := binaries
RELDIR := releases
PLATFORMS ?= linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64,windows/arm64,freebsd/amd64,freebsd/arm64
REGISTRY ?= ghcr.io/$(PROJECT_ORG)/$(PROJECT_NAME)
GO_CACHE ?= $(HOME)/go/pkg/mod
GO_BUILD ?= $(HOME)/.cache/go-build/$(PROJECT_NAME)
DOCKER_MEM ?= 4g
DOCKER_CPUS ?= 2
DOCKER_TIMEOUT ?= 3600
GO_DOCKER_RUN := timeout -k 30 $(DOCKER_TIMEOUT) docker run --rm --no-healthcheck --name $(PROJECT_NAME)-$$(tr -dc 'a-z0-9' </dev/urandom | head -c8) --memory=$(DOCKER_MEM) --cpus=$(DOCKER_CPUS) -v "$(CURDIR):/app" -v "$(GO_CACHE):/usr/local/share/go/pkg/mod" -v "$(GO_BUILD):/usr/local/share/go/cache" -w /app -e CGO_ENABLED=0 -e GOFLAGS=-buildvcs=false
GO_DOCKER := $(GO_DOCKER_RUN) casjaysdev/go:latest
GO_DOCKER_BUILD := $(GO_DOCKER_RUN) -v "$(abspath $(BINDIR)):/build" casjaysdev/go:latest

.PHONY: dev local build test release docker clean
.DEFAULT_GOAL := build

build: clean
	@mkdir -p $(BINDIR) "$(GO_CACHE)" "$(GO_BUILD)"
	@$(GO_DOCKER_BUILD) sh -c 'set -eu; go mod download; \
		go build -buildvcs=false -trimpath -ldflags "$(LDFLAGS)" -o "/build/$(PROJECT_NAME)" ./src; \
		go build -buildvcs=false -trimpath -ldflags "-s -w -X main.ProjectName=$(PROJECT_NAME) -X main.InternalName=$(INTERNAL_NAME) -X main.Version=$(VERSION) -X main.CommitID=$(COMMIT_ID) -X main.BuildEpoch=$(BUILD_EPOCH) -X main.OfficialSite=$(OFFICIAL_SITE)" -o "/build/$(PROJECT_NAME)-cli" ./src/client; \
		for platform in $$(printf "%s" "$(PLATFORMS)" | tr "," " "); do \
			GOOS=$${platform%/*}; GOARCH=$${platform#*/}; output="/build/$(PROJECT_NAME)-$$GOOS-$$GOARCH"; \
			cli_output="/build/$(PROJECT_NAME)-cli-$$GOOS-$$GOARCH"; \
			if [ "$$GOOS" = windows ]; then output=$$output.exe; cli_output=$$cli_output.exe; fi; \
			echo "Building $$GOOS/$$GOARCH"; \
			GOOS=$$GOOS GOARCH=$$GOARCH go build -buildvcs=false -trimpath -ldflags "$(LDFLAGS)" -o "$$output" ./src; \
			GOOS=$$GOOS GOARCH=$$GOARCH go build -buildvcs=false -trimpath -ldflags "-s -w -X main.ProjectName=$(PROJECT_NAME) -X main.InternalName=$(INTERNAL_NAME) -X main.Version=$(VERSION) -X main.CommitID=$(COMMIT_ID) -X main.BuildEpoch=$(BUILD_EPOCH) -X main.OfficialSite=$(OFFICIAL_SITE)" -o "$$cli_output" ./src/client; \
		done'

local: clean
	@mkdir -p $(BINDIR) "$(GO_CACHE)" "$(GO_BUILD)"
	@$(GO_DOCKER_BUILD) sh -c 'set -eu; go mod download; \
		GOOS=$$(go env GOOS) GOARCH=$$(go env GOARCH) go build -buildvcs=false -trimpath -ldflags "$(LDFLAGS)" -o /build/$(PROJECT_NAME) ./src; \
		GOOS=$$(go env GOOS) GOARCH=$$(go env GOARCH) go build -buildvcs=false -trimpath -ldflags "-s -w -X main.ProjectName=$(PROJECT_NAME) -X main.InternalName=$(INTERNAL_NAME) -X main.Version=$(VERSION) -X main.CommitID=$(COMMIT_ID) -X main.BuildEpoch=$(BUILD_EPOCH) -X main.OfficialSite=$(OFFICIAL_SITE)" -o /build/$(PROJECT_NAME)-cli ./src/client'

# Development binaries are written only into an organization-scoped temp directory.
dev:
	@mkdir -p "$${TMPDIR:-/tmp}/$(PROJECT_ORG)" "$(GO_CACHE)" "$(GO_BUILD)"
	@BUILD_DIR=$$(mktemp -d "$${TMPDIR:-/tmp}/$(PROJECT_ORG)/$(INTERNAL_NAME)-XXXXXX") && \
		$(GO_DOCKER_RUN) -v "$$BUILD_DIR:/build" casjaysdev/go:latest sh -c 'set -eu; go mod download; \
			go build -buildvcs=false -o /build/$(PROJECT_NAME) ./src; \
			go build -buildvcs=false -o /build/$(PROJECT_NAME)-cli ./src/client' && \
		echo "Development binaries: $$BUILD_DIR/"

test:
	@set -eu; network_suffix=$$(tr -dc 'a-z0-9' </dev/urandom | head -c8); \
		TEST_NETWORK="$(PROJECT_NAME)-test-net-$$network_suffix"; \
		mkdir -p "$${TMPDIR:-/tmp}/$(PROJECT_ORG)"; \
		TEST_DIR=$$(mktemp -d "$${TMPDIR:-/tmp}/$(PROJECT_ORG)/$(INTERNAL_NAME)-XXXXXX"); \
		docker network create --driver bridge "$$TEST_NETWORK" >/dev/null; \
		trap 'docker network rm "$$TEST_NETWORK" >/dev/null 2>&1 || true; rm -rf "$$TEST_DIR"' EXIT; \
		$(GO_DOCKER_RUN) --network "$$TEST_NETWORK" -v "$$TEST_DIR:/test" casjaysdev/go:latest sh -c 'set -eu; mkdir -p /test/tmp; export TMPDIR=/test/tmp; go vet ./...; govulncheck ./...; go test -coverprofile=/test/coverage.out ./...; COVERAGE=$$(awk '\''NR > 1 { total += $$2; if ($$3 > 0) covered += $$2 } END { if (total == 0) exit 1; printf "%.1f", covered * 100 / total }'\'' /test/coverage.out); awk -v coverage="$$COVERAGE" '\''BEGIN { if (coverage < 60) { printf "coverage %.1f%% is below the 60%% minimum\n", coverage; exit 1 } }'\''; printf "Coverage: %s%%\n" "$$COVERAGE"'

release: build
	@mkdir -p $(RELDIR)
	@printf '%s\n' "$(VERSION)" > $(RELDIR)/version.txt
	@cp $(BINDIR)/$(PROJECT_NAME)-* $(RELDIR)/
	@tar --exclude='.git' --exclude='.github' --exclude='.gitea' --exclude='.forgejo' \
		--exclude='binaries' --exclude='releases' --exclude='*.tar.gz' \
		-czf $(RELDIR)/$(PROJECT_NAME)-$(VERSION)-source.tar.gz .
	@cd $(RELDIR) && sha256sum $(PROJECT_NAME)-linux-* $(PROJECT_NAME)-darwin-* $(PROJECT_NAME)-windows-* $(PROJECT_NAME)-freebsd-* $(PROJECT_NAME)-cli-linux-* $(PROJECT_NAME)-cli-darwin-* $(PROJECT_NAME)-cli-windows-* $(PROJECT_NAME)-cli-freebsd-* > sha256.txt
	@cd $(RELDIR) && sha512sum $(PROJECT_NAME)-linux-* $(PROJECT_NAME)-darwin-* $(PROJECT_NAME)-windows-* $(PROJECT_NAME)-freebsd-* $(PROJECT_NAME)-cli-linux-* $(PROJECT_NAME)-cli-darwin-* $(PROJECT_NAME)-cli-windows-* $(PROJECT_NAME)-cli-freebsd-* > sha512.txt
	@RELEASE_TAG=$$(case "$(VERSION)" in v*) printf '%s' "$(VERSION)" ;; *) \
		if printf '%s' "$(VERSION)" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+'; then printf 'v%s' "$(VERSION)"; \
		else printf '%s' "$(VERSION)"; fi ;; esac); \
		gh release delete "$$RELEASE_TAG" --yes --cleanup-tag 2>/dev/null || true; \
		gh release create "$$RELEASE_TAG" $(RELDIR)/* --title "$$RELEASE_TAG" --notes "Release $$RELEASE_TAG" --latest

# OCI metadata is supplied at build time; the Dockerfile contains no LABEL directives.
docker:
	@docker buildx version >/dev/null 2>&1 || { echo "docker buildx required" >&2; exit 1; }
	@docker buildx create --name multiarch-builder --use 2>/dev/null || docker buildx use multiarch-builder
	@docker buildx build -f docker/Dockerfile --platform linux/amd64,linux/arm64 --push \
		--build-arg VERSION="$(VERSION)" --build-arg BUILD_EPOCH="$(BUILD_EPOCH)" --build-arg COMMIT_ID="$(COMMIT_ID)" \
		--build-arg OFFICIAL_SITE="$(OFFICIAL_SITE)" \
		--label "org.opencontainers.image.version=$(VERSION)" \
		--label "org.opencontainers.image.revision=$(COMMIT_ID)" \
		--label "org.opencontainers.image.created=$(BUILD_DATE)" \
		--label "org.opencontainers.image.licenses=MIT" \
		-t $(REGISTRY):$(VERSION) -t $(REGISTRY):latest .

clean:
	@rm -rf $(BINDIR) $(RELDIR)
