DIST_DIR := dist
AGENT_BIN := wattkeeper-agent
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
RELEASE_DIR := $(DIST_DIR)/release
DOCS_VENV := .venv
DOCS_MKDOCS := $(DOCS_VENV)/bin/mkdocs
DOCS_STAMP := $(DOCS_VENV)/.docs-installed

.PHONY: agent release-agent test lint image docs-setup docs-build docs-serve sim-up sim-down

agent:
	@mkdir -p $(DIST_DIR)
	GOOS=linux GOARCH=arm64 go build -ldflags "-X main.version=$(VERSION)" -o $(DIST_DIR)/$(AGENT_BIN)-linux-arm64 ./agent/cmd/agent
	GOOS=linux GOARCH=arm GOARM=6 go build -ldflags "-X main.version=$(VERSION)" -o $(DIST_DIR)/$(AGENT_BIN)-linux-armv6 ./agent/cmd/agent

release-agent: agent
	@rm -rf $(RELEASE_DIR)
	@mkdir -p $(RELEASE_DIR)
	@for arch in linux-arm64 linux-armv6; do \
		stage="$(RELEASE_DIR)/$(AGENT_BIN)-$(VERSION)-$$arch"; \
		mkdir -p "$$stage/deploy"; \
		install -m 0755 "$(DIST_DIR)/$(AGENT_BIN)-$$arch" "$$stage/$(AGENT_BIN)"; \
		install -m 0644 agent/README.md "$$stage/README.md"; \
		install -m 0755 deploy/install.sh "$$stage/deploy/install.sh"; \
		install -m 0644 deploy/wattkeeper-agent.service "$$stage/deploy/wattkeeper-agent.service"; \
		install -m 0644 deploy/99-wattkeeper-agent.rules "$$stage/deploy/99-wattkeeper-agent.rules"; \
		tar -C "$(RELEASE_DIR)" -czf "$(RELEASE_DIR)/$(AGENT_BIN)-$(VERSION)-$$arch.tar.gz" "$(AGENT_BIN)-$(VERSION)-$$arch"; \
		rm -rf "$$stage"; \
	done
	@cd "$(RELEASE_DIR)" && sha256sum *.tar.gz > SHA256SUMS

test:
	cd agent && go test ./...
	cd controller && go test ./...

lint:
	cd agent && golangci-lint run ./...
	cd controller && golangci-lint run ./...

image: agent
	./image/build.sh "$(VERSION)"

$(DOCS_STAMP): docs/requirements.txt
	python3 -m venv $(DOCS_VENV)
	$(DOCS_VENV)/bin/pip install -r docs/requirements.txt
	@touch $(DOCS_STAMP)

docs-setup: $(DOCS_STAMP)

docs-build: $(DOCS_STAMP)
	$(DOCS_MKDOCS) build

docs-serve: $(DOCS_STAMP)
	$(DOCS_MKDOCS) serve

sim-up sim-down:
	@echo not implemented