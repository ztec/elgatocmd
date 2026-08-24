.DEFAULT_GOAL := build

BOX := elgatolight-dev
DISTROBOX_FILE := $(CURDIR)/distrobox.ini
BUILD_IMAGE ?= localhost/elgatolight-build:dev
CONTAINER_ENGINE ?= $(shell if command -v podman >/dev/null 2>&1; then printf podman; elif command -v docker >/dev/null 2>&1; then printf docker; fi)
CONTAINER_MANAGER_NAME := $(notdir $(CONTAINER_ENGINE))
RUNTIME_DIR := $(if $(XDG_RUNTIME_DIR),$(XDG_RUNTIME_DIR),/run/user/$(shell id -u))
HOST_DBUS_SESSION_BUS_ADDRESS := $(DBUS_SESSION_BUS_ADDRESS)
PODMAN_RUNTIME_ARG := $(if $(shell command -v runc 2>/dev/null),--runtime=runc)
PODMAN_ARGS := --cgroup-manager=cgroupfs $(PODMAN_RUNTIME_ARG)
CONTAINER := $(if $(filter podman,$(CONTAINER_MANAGER_NAME)),env DBUS_SESSION_BUS_ADDRESS=unix:path=$(RUNTIME_DIR)/elgatolight-container-no-bus )$(CONTAINER_ENGINE) $(if $(filter podman,$(CONTAINER_MANAGER_NAME)),$(PODMAN_ARGS))
CONTAINER_USER_ARGS := $(if $(filter podman,$(CONTAINER_MANAGER_NAME)),,--user "$$(id -u):$$(id -g)")
DISTROBOX := env DBX_CONTAINER_MANAGER=$(CONTAINER_MANAGER_NAME) DBUS_SESSION_BUS_ADDRESS=unix:path=$(RUNTIME_DIR)/elgatolight-distrobox-no-bus distrobox
RESTORE_HOST_DBUS := env DBUS_SESSION_BUS_ADDRESS='$(HOST_DBUS_SESSION_BUS_ADDRESS)'
PYTHON := /opt/elgatolight-venv/bin/python
DEV_ARGS ?= watch
VERSION ?=
DIST_DIR ?= dist

.PHONY: build image container-build container-test release box shell dev build-in-box release-in-box test-in-box test-go-in-box test-python-in-box check-container-engine check-version

build: box
	@printf '%s\n' '[build] Entering the development container...'
	@$(DISTROBOX) enter $(BOX) -- $(RESTORE_HOST_DBUS) bash --noprofile --norc -c 'cd "$(CURDIR)" && $(MAKE) build-in-box'

check-container-engine:
	@if [ -z "$(CONTAINER_ENGINE)" ]; then \
		printf '%s\n' 'error: Podman or Docker is required (or set CONTAINER_ENGINE)'; \
		exit 1; \
	fi
	@if ! command -v "$(CONTAINER_ENGINE)" >/dev/null 2>&1; then \
		printf 'error: configured container engine not found: %s\n' "$(CONTAINER_ENGINE)"; \
		exit 1; \
	fi

check-version:
	@if ! printf '%s\n' "$(VERSION)" | grep -Eq '^[0-9]+\.[0-9]+$$'; then \
		printf 'error: VERSION must match MAJOR.MINOR exactly (for example 1.2); got %s\n' "$(if $(VERSION),$(VERSION),<empty>)"; \
		exit 2; \
	fi

image: check-container-engine
	@printf '[image] Building %s with %s...\n' "$(BUILD_IMAGE)" "$(CONTAINER_ENGINE)"
	@$(CONTAINER) build --file Dockerfile --tag "$(BUILD_IMAGE)" .

container-build: image
	@printf '[container] Running the complete build with %s...\n' "$(CONTAINER_ENGINE)"
	@$(CONTAINER) run --rm \
		$(CONTAINER_USER_ARGS) \
		--env HOME=/tmp/elgatolight-home \
		--env GOCACHE=/tmp/elgatolight-go-build \
		--volume "$(CURDIR):/workspace:Z" \
		--workdir /workspace \
		"$(BUILD_IMAGE)" make build-in-box

container-test: image
	@printf '[container] Running all tests with %s...\n' "$(CONTAINER_ENGINE)"
	@$(CONTAINER) run --rm \
		$(CONTAINER_USER_ARGS) \
		--env HOME=/tmp/elgatolight-home \
		--env GOCACHE=/tmp/elgatolight-go-build \
		--volume "$(CURDIR):/workspace:Z" \
		--workdir /workspace \
		"$(BUILD_IMAGE)" make test-in-box

release: check-version image
	@printf '[container] Building release %s with %s...\n' "$(VERSION)" "$(CONTAINER_ENGINE)"
	@$(CONTAINER) run --rm \
		$(CONTAINER_USER_ARGS) \
		--env HOME=/tmp/elgatolight-home \
		--env GOCACHE=/tmp/elgatolight-go-build \
		--env VERSION="$(VERSION)" \
		--env DIST_DIR="$(DIST_DIR)" \
		--volume "$(CURDIR):/workspace:Z" \
		--workdir /workspace \
		"$(BUILD_IMAGE)" make release-in-box VERSION="$(VERSION)" DIST_DIR="$(DIST_DIR)"

box: image
	@printf '%s\n' '[box] Creating or updating $(BOX)...'
	@$(DISTROBOX) assemble create --file "$(DISTROBOX_FILE)" --name $(BOX)

shell: box
	@printf '%s\n' '[shell] Entering $(BOX)...'
	@$(DISTROBOX) enter $(BOX) -- $(RESTORE_HOST_DBUS) bash -l

dev: box
	@printf '%s\n' '[dev] Starting elgatolight $(DEV_ARGS)...'
	@$(DISTROBOX) enter $(BOX) -- $(RESTORE_HOST_DBUS) bash --noprofile --norc -c 'cd "$(CURDIR)" && go run ./cmd/elgatolight $(DEV_ARGS)'

build-in-box: test-in-box
	@printf '%s\n' '[build 5/5] Compiling bin/elgatolight...'
	mkdir -p bin
	go build -trimpath -o bin/elgatolight ./cmd/elgatolight
	@printf '%s\n' '[build] Complete: bin/elgatolight'

release-in-box: check-version test-in-box
	@printf '[release] Packaging version %s...\n' "$(VERSION)"
	@VERSION="$(VERSION)" DIST_DIR="$(DIST_DIR)" ./scripts/build-release.sh

test-in-box: test-go-in-box test-python-in-box

test-go-in-box:
	@printf '%s\n' '[build 1/5] Running Go static analysis...'
	go vet ./...
	@printf '%s\n' '[build 2/5] Running Go tests...'
	go test ./...

test-python-in-box:
	@printf '%s\n' '[build 3/5] Python test dependencies are provided by the build image.'
	@printf '%s\n' '[build 4/5] Running Home Assistant integration tests...'
	$(PYTHON) -m compileall -q custom_components
	$(PYTHON) -m pytest -q tests
