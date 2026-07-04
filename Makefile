DIST_DIR := dist
AGENT_BIN := wattkeeper-agent
CONTROLLER_BIN := wattkeeper-controller
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
RELEASE_DIR := $(DIST_DIR)/release
UV ?= uv
DOCS_UV_RUN := $(UV) run --locked --group docs
NODE_DEV_UI_LISTEN ?= 127.0.0.1:8080
NODE_DEV_UI_FLAGS ?=
CONTROLLER_IMAGE ?= wattkeeper-controller:$(VERSION)
CONTROLLER_IMAGE_GHCR ?= ghcr.io/foehammer82/wattkeeper-controller:$(VERSION)

.PHONY: agent controller release-agent test lint image docs-setup docs-build docs-serve node-dev-ui node-dev-ui-open controller-dev controller-web controller-web-install controller-image controller-image-multiarch sim-up sim-down

agent:
	@mkdir -p $(DIST_DIR)
	GOOS=linux GOARCH=arm64 go build -ldflags "-X main.version=$(VERSION)" -o $(DIST_DIR)/$(AGENT_BIN)-linux-arm64 ./agent/cmd/agent
	GOOS=linux GOARCH=arm GOARM=6 go build -ldflags "-X main.version=$(VERSION)" -o $(DIST_DIR)/$(AGENT_BIN)-linux-armv6 ./agent/cmd/agent

controller:
	@mkdir -p $(DIST_DIR)
	$(MAKE) controller-web
	go build -ldflags "-X main.version=$(VERSION)" -o $(DIST_DIR)/$(CONTROLLER_BIN) ./controller/cmd/controller

controller-dev:
	go run ./controller/cmd/controller --data-dir ./controller/dist/data --listen :9000

controller-web-install:
	cd controller/web && npm install

controller-web:
	cd controller/web && npm run build
	rm -rf controller/cmd/controller/assets
	mkdir -p controller/cmd/controller/assets
	cp -R controller/web/dist/. controller/cmd/controller/assets/

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

docs-setup: pyproject.toml
	$(UV) sync --locked --group docs

docs-build: pyproject.toml
	$(DOCS_UV_RUN) mkdocs build

docs-serve: pyproject.toml
	$(DOCS_UV_RUN) mkdocs serve

node-dev-ui:
	go run ./agent/cmd/agent --dev-ui --listen $(NODE_DEV_UI_LISTEN) $(NODE_DEV_UI_FLAGS)

node-dev-ui-open:
	go run ./agent/cmd/agent --dev-ui --listen $(NODE_DEV_UI_LISTEN) --http-auth=false $(NODE_DEV_UI_FLAGS)

controller-image:
	docker build --build-arg VERSION=$(VERSION) -f controller/Dockerfile -t $(CONTROLLER_IMAGE) .

controller-image-multiarch:
	docker buildx build --platform linux/amd64,linux/arm64 --build-arg VERSION=$(VERSION) -f controller/Dockerfile -t $(CONTROLLER_IMAGE_GHCR) .

sim-up sim-down:
	@echo not implemented