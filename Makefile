.DEFAULT_GOAL := help

BINARY := elgatolight
COMMAND := ./cmd/elgatolight
BOX := elgatolight-dev
DISTROBOX_FILE := $(CURDIR)/distrobox.ini
DEV_IMAGE ?= localhost/elgatolight-dev:local
RUNTIME_IMAGE ?= localhost/elgatolight:dev
CONTAINER_ENGINE ?= $(shell if command -v podman >/dev/null 2>&1; then printf podman; elif command -v docker >/dev/null 2>&1; then printf docker; fi)
CONTAINER_ENGINE_NAME := $(notdir $(CONTAINER_ENGINE))
COMMA := ,
WORKTREE_VOLUME_OPTIONS := $(if $(filter podman,$(CONTAINER_ENGINE_NAME)),rw$(COMMA)Z,rw)
DISTROBOX := env DBX_CONTAINER_MANAGER=$(CONTAINER_ENGINE_NAME) distrobox
PYTHON := /opt/elgatolight-venv/bin/python
VERSION ?= dev
VERSION_FILE ?=
VERSION_COMPONENTS ?= 2
DIST_DIR ?= dist
COPIER_IMAGE ?= localhost/tmplt-copier:9.17.1
COPIER_FILE := tools/copier/Containerfile
# renovate: datasource=docker depName=docker.io/library/golang versioning=docker
GO_SECURITY_VERSION ?= 1.26.6
# renovate: datasource=go depName=golang.org/x/vuln
GOVULNCHECK_VERSION ?= v1.6.0

.PHONY: help test build run security ci test-native build-native run-native security-native ci-native fmt fmt-check vet verify unit race coverage python-test scripts-check release-version-test cross-build template-contract image image-runtime copier-image container-test container-security container-ci container-build container-deps-tidy container-smoke deps-tidy box box-replace shell tmplt-check tmplt-update tmplt-update-preflight tmplt-update-validate check-container-engine check-version release release-in-container clean

help:
	@printf '%s\n' \
		'Elgato Key Light Neo USB controller development commands (Podman or Docker is the only required tool):' \
		'  make test          Run Go, Home Assistant, script, release, and cross-build checks' \
		'  make security      Scan reachable Go code for known vulnerabilities' \
		'  make ci            Run test and security exactly as CI does' \
		'  make build         Produce bin/elgatolight' \
		'  make run           Build and run the minimal runtime image' \
		'  make shell         Enter the optional Distrobox development shell' \
		'  make tmplt-check   Report the installed and latest template versions' \
		'  make tmplt-update  Apply, normalize, and test the latest template release' \
		'  make release VERSION=v0.1   Build a signed release using the exported RELEASE_SIGNING_KEY'

test: container-test

security: container-security

ci: container-ci

build: container-build

run: image-runtime
	$(CONTAINER_ENGINE) run --rm "$(RUNTIME_IMAGE)"

fmt:
	gofmt -w cmd internal packaging scripts

fmt-check:
	@test -z "$$(gofmt -l $$(find cmd internal packaging scripts -name '*.go' -type f))" || { \
		printf '%s\n' 'error: Go files need gofmt'; \
		gofmt -l $$(find cmd internal packaging scripts -name '*.go' -type f); \
		exit 1; \
	}

vet:
	go vet ./...

verify:
	go mod verify

unit:
	go test -shuffle=on ./...

race:
	go test -race ./...

coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

python-test:
	$(PYTHON) -m compileall -q custom_components
	$(PYTHON) -m pytest -q tests

scripts-check:
	@sh -n install.sh
	@for script in scripts/*.sh; do sh -n "$$script"; done

release-version-test:
	@./scripts/test-next-release-version.sh
	@./scripts/test-release-policy.sh
	@./scripts/test-release-notes.sh
	@./scripts/test-release-existence-policy.sh
	@./scripts/test-renovate-tmplt-ready.sh

cross-build:
	@set -eu; \
	output=$$(mktemp -d); \
	trap 'rm -rf "$$output"' EXIT HUP INT TERM; \
	for target in linux/amd64 linux/arm64 linux/arm; do \
		goos=$${target%/*}; goarch=$${target#*/}; name=$${goos}-$${goarch}; \
		case $$target in linux/arm) CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -buildvcs=false -trimpath -o "$$output/$$name" $(COMMAND) ;; \
		*) CGO_ENABLED=0 GOOS="$$goos" GOARCH="$$goarch" go build -buildvcs=false -trimpath -o "$$output/$$name" $(COMMAND) ;; \
		esac; \
	done

template-contract:
	@./scripts/tmplt-update-validate.sh

test-native: fmt-check vet verify unit race coverage python-test scripts-check release-version-test cross-build template-contract

security-native:
	GOTOOLCHAIN=go$(GO_SECURITY_VERSION) go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

ci-native: test-native security-native

build-native: test-native
	@mkdir -p bin
	go build -buildvcs=false -trimpath -ldflags '-X git2.riper.fr/ztec/elgatocmd/internal/buildinfo.Version=$(VERSION)' -o bin/$(BINARY) $(COMMAND)

run-native:
	go run $(COMMAND)

check-container-engine:
	@if [ -z "$(CONTAINER_ENGINE)" ] || ! command -v "$(CONTAINER_ENGINE)" >/dev/null 2>&1; then \
		printf '%s\n' 'error: Podman or Docker is required (or set CONTAINER_ENGINE)'; \
		exit 1; \
	fi

image: check-container-engine
	$(CONTAINER_ENGINE) build --target dev --tag "$(DEV_IMAGE)" .

image-runtime: check-container-engine
	$(CONTAINER_ENGINE) build --target runtime --build-arg VERSION="$(VERSION)" --tag "$(RUNTIME_IMAGE)" .

copier-image: check-container-engine
	$(CONTAINER_ENGINE) build --tag "$(COPIER_IMAGE)" --file "$(COPIER_FILE)" tools/copier

container-test: image
	$(CONTAINER_ENGINE) run --rm --env HOME=/tmp/elgatolight-home --env GOCACHE=/tmp/elgatolight-go-build --workdir /workspace "$(DEV_IMAGE)" make test-native

container-security: image
	$(CONTAINER_ENGINE) run --rm --env HOME=/tmp/elgatolight-home --env GOCACHE=/tmp/elgatolight-go-build --workdir /workspace "$(DEV_IMAGE)" make security-native

container-ci: image
	$(CONTAINER_ENGINE) run --rm --env HOME=/tmp/elgatolight-home --env GOCACHE=/tmp/elgatolight-go-build --workdir /workspace "$(DEV_IMAGE)" make ci-native

container-build: image
	@set -eu; \
	container_id=$$( $(CONTAINER_ENGINE) create --env HOME=/tmp/elgatolight-home --env GOCACHE=/tmp/elgatolight-go-build --workdir /workspace "$(DEV_IMAGE)" make build-native VERSION="$(VERSION)" ); \
	cleanup() { $(CONTAINER_ENGINE) rm --force "$$container_id" >/dev/null 2>&1 || true; }; \
	trap cleanup EXIT HUP INT TERM; \
	$(CONTAINER_ENGINE) start --attach "$$container_id"; \
	mkdir -p bin; \
	$(CONTAINER_ENGINE) cp "$$container_id:/workspace/bin/$(BINARY)" "bin/$(BINARY)"

container-deps-tidy: image
	@set -eu; \
	case "$(CONTAINER_ENGINE_NAME)" in \
		podman) user_options='--userns=keep-id' ;; \
		docker) user_options="--user $$(id -u):$$(id -g)" ;; \
		*) printf '%s\n' 'error: dependency normalization supports Podman or Docker'; exit 2 ;; \
	esac; \
	$(CONTAINER_ENGINE) run --rm $$user_options \
		--volume "$(CURDIR):/workspace:$(WORKTREE_VOLUME_OPTIONS)" \
		--env HOME=/tmp/elgatolight-home --env GOCACHE=/tmp/elgatolight-go-build \
		--workdir /workspace "$(DEV_IMAGE)" go mod tidy

deps-tidy: container-deps-tidy

container-smoke: image-runtime
	@actual="$$( $(CONTAINER_ENGINE) run --rm "$(RUNTIME_IMAGE)" --version )"; \
	test "$$actual" = "$(VERSION)" || { printf 'error: runtime image reports %s, expected %s\n' "$$actual" "$(VERSION)"; exit 1; }; \
	printf '[container] Runtime image reports %s\n' "$$actual"

box: image
	@command -v distrobox >/dev/null 2>&1 || { printf '%s\n' 'error: distrobox is required'; exit 1; }
	$(DISTROBOX) assemble create --file "$(DISTROBOX_FILE)" --name "$(BOX)"

box-replace: image
	@command -v distrobox >/dev/null 2>&1 || { printf '%s\n' 'error: distrobox is required'; exit 1; }
	$(DISTROBOX) assemble create --replace --file "$(DISTROBOX_FILE)" --name "$(BOX)"

shell: box
	$(DISTROBOX) enter "$(BOX)" -- bash -l

tmplt-check: copier-image
	@set -eu; \
	case "$(CONTAINER_ENGINE_NAME)" in \
		podman) user_options='--userns=keep-id' ;; \
		docker) user_options="--user $$(id -u):$$(id -g)" ;; \
		*) printf '%s\n' 'error: template updates support Podman or Docker'; exit 2 ;; \
	esac; \
	$(CONTAINER_ENGINE) run --rm $$user_options \
		--volume "$(CURDIR):/workspace:$(WORKTREE_VOLUME_OPTIONS)" \
		--env HOME=/tmp/tmplt-copier-home \
		--workdir /workspace --entrypoint sh "$(COPIER_IMAGE)" \
		./scripts/tmplt-source.sh check

tmplt-update-preflight:
	@test -f .copier-answers.yml || { printf '%s\n' 'error: .copier-answers.yml is missing'; exit 2; }
	@test -z "$$(git status --porcelain --untracked-files=normal)" || { printf '%s\n' 'error: template update requires a clean worktree'; exit 2; }

tmplt-update: tmplt-update-preflight copier-image
	@set -eu; \
	case "$(CONTAINER_ENGINE_NAME)" in \
		podman) user_options='--userns=keep-id' ;; \
		docker) user_options="--user $$(id -u):$$(id -g)" ;; \
		*) printf '%s\n' 'error: template updates support Podman or Docker'; exit 2 ;; \
	esac; \
	latest=$$( $(CONTAINER_ENGINE) run --rm $$user_options \
		--volume "$(CURDIR):/workspace:$(WORKTREE_VOLUME_OPTIONS)" \
		--env HOME=/tmp/tmplt-copier-home \
		--workdir /workspace --entrypoint sh "$(COPIER_IMAGE)" \
		./scripts/tmplt-source.sh latest ); \
	$(CONTAINER_ENGINE) run --rm $$user_options \
		--volume "$(CURDIR):/workspace:$(WORKTREE_VOLUME_OPTIONS)" \
		--env HOME=/tmp/tmplt-copier-home \
		--workdir /workspace "$(COPIER_IMAGE)" \
		update --vcs-ref "$$latest" --defaults --skip-answered --conflict rej /workspace; \
	./scripts/tmplt-source.sh set-module "$$latest"
	@$(MAKE) tmplt-update-validate
	@$(MAKE) deps-tidy
	@$(MAKE) ci
	@printf '%s\n' 'Template update applied and tested; review and commit the working-tree changes.'

tmplt-update-validate:
	@./scripts/tmplt-source.sh set-module "$$(./scripts/tmplt-source.sh current)"
	@./scripts/tmplt-update-validate.sh

check-version:
	@set -eu; \
	if [ -n "$(VERSION_FILE)" ]; then version=$$(cat "$(VERSION_FILE)"); else version="$(VERSION)"; fi; \
	printf '%s\n' "$$version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(\.(0|[1-9][0-9]*))?(-[0-9A-Za-z][0-9A-Za-z.-]*)?$$' || { printf 'error: invalid release version %s\n' "$$version"; exit 2; }; \
	test -n "$${RELEASE_SIGNING_KEY:-}" || { printf '%s\n' 'error: RELEASE_SIGNING_KEY is required'; exit 2; }; \
	test -z "$$(git status --porcelain --untracked-files=normal)" || { printf '%s\n' 'error: release requires a clean worktree'; exit 2; }

release: check-version image
	@set -eu; \
	if [ -n "$(VERSION_FILE)" ]; then version=$$(cat "$(VERSION_FILE)"); else version="$(VERSION)"; fi; \
	source_date_epoch=$$(git show -s --format=%ct HEAD); \
	container_id=$$( $(CONTAINER_ENGINE) create --env RELEASE_SIGNING_KEY --env RELEASE_SIGNING_KEYS_DIRECTORY --env HOME=/tmp/elgatolight-home --env GOCACHE=/tmp/elgatolight-go-build --workdir /workspace "$(DEV_IMAGE)" make release-in-container VERSION="$$version" VERSION_FILE= SOURCE_DATE_EPOCH="$$source_date_epoch" DIST_DIR=/tmp/elgatolight-release ); \
	cleanup() { $(CONTAINER_ENGINE) rm --force "$$container_id" >/dev/null 2>&1 || true; }; \
	trap cleanup EXIT HUP INT TERM; \
	$(CONTAINER_ENGINE) start --attach "$$container_id"; \
	mkdir -p "$(DIST_DIR)"; \
	$(CONTAINER_ENGINE) cp "$$container_id:/tmp/elgatolight-release/." "$(DIST_DIR)"

release-in-container: test-native
	@VERSION="$(VERSION)" VERSION_FILE="$(VERSION_FILE)" SOURCE_DATE_EPOCH="$(SOURCE_DATE_EPOCH)" DIST_DIR="$(DIST_DIR)" ./scripts/build-release.sh

clean:
	rm -rf bin dist coverage.out
