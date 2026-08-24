.DEFAULT_GOAL := build

BOX := elgatolight-dev
DISTROBOX_FILE := $(CURDIR)/distrobox.ini
UDEV_RULE_SOURCE := $(CURDIR)/packaging/99-elgato-key-light-neo.rules
UDEV_RULE_DESTINATION := /etc/udev/rules.d/99-elgato-key-light-neo.rules
SUDO ?= $(if $(filter 0,$(shell id -u)),,sudo)
PYTHON_ENV := .venv
PYTHON := $(PYTHON_ENV)/bin/python
PYTHON_STAMP := $(PYTHON_ENV)/.requirements-stamp
DEV_ARGS ?= watch

.PHONY: build box shell dev setup build-in-box test-in-box test-go-in-box test-python-in-box

build: box
	distrobox enter $(BOX) -- bash -lc 'cd "$(CURDIR)" && $(MAKE) build-in-box'

box:
	distrobox assemble create --file "$(DISTROBOX_FILE)" --name $(BOX)

shell: box
	distrobox enter $(BOX)

dev: box
	distrobox enter $(BOX) -- bash -lc 'cd "$(CURDIR)" && go run ./cmd/elgatolight $(DEV_ARGS)'

setup:
	$(SUDO) install -m 0644 "$(UDEV_RULE_SOURCE)" "$(UDEV_RULE_DESTINATION)"
	$(SUDO) udevadm control --reload-rules
	@printf '%s\n' 'udev rule installed; unplug and reconnect the Key Light Neo.'

build-in-box: test-in-box
	mkdir -p bin
	go build -trimpath -o bin/elgatolight ./cmd/elgatolight

test-in-box: test-go-in-box test-python-in-box

test-go-in-box:
	go vet ./...
	go test ./...

test-python-in-box: $(PYTHON_STAMP)
	$(PYTHON) -m compileall -q custom_components
	$(PYTHON) -m pytest -q tests

$(PYTHON_STAMP): requirements-dev.txt
	python3 -m venv $(PYTHON_ENV)
	$(PYTHON) -m pip install --upgrade pip
	$(PYTHON) -m pip install --requirement requirements-dev.txt
	touch $(PYTHON_STAMP)
