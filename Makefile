DIST_DIR := dist
AGENT_BIN := wattkeeper-agent

.PHONY: agent test lint image sim-up sim-down

agent:
	@mkdir -p $(DIST_DIR)
	GOOS=linux GOARCH=arm64 go build -o $(DIST_DIR)/$(AGENT_BIN)-linux-arm64 ./agent/cmd/agent
	GOOS=linux GOARCH=arm GOARM=6 go build -o $(DIST_DIR)/$(AGENT_BIN)-linux-armv6 ./agent/cmd/agent

test:
	cd agent && go test ./...
	cd controller && go test ./...

lint:
	cd agent && golangci-lint run ./...
	cd controller && golangci-lint run ./...

image sim-up sim-down:
	@echo not implemented