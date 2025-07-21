# Makefile for DNS service project
# Builds server, client, czds, query, and UI components

# Variables
GO=go
NPM=npm
BUF=buf
DOCKER=docker
BINARY_DIR=bin
SERVER_BINARY=$(BINARY_DIR)/server
CZDS_BINARY=$(BINARY_DIR)/czds
QUERY_BINARY=$(BINARY_DIR)/query
CLIENT_TEST_BINARY=$(BINARY_DIR)/client_test
CONFIG=config.yaml
SERVER_IMAGE=dns-service-server:latest
UI_DIR=ui
ZONES_DIR=/zones

# Default target
.PHONY: all
all: build

# Create binary directory
$(BINARY_DIR):
	mkdir -p $(BINARY_DIR)

# Generate Protobuf code
.PHONY: proto
proto:
	$(BUF) mod update
	$(BUF) generate

# Build Go binaries
.PHONY: build
build: $(BINARY_DIR) proto build-server build-czds build-query build-client-test

.PHONY: build-server
build-server:
	$(GO) build -o $(SERVER_BINARY) ./server

.PHONY: build-czds
build-czds:
	$(GO) build -o $(CZDS_BINARY) ./czds

.PHONY: build-query
build-query:
	$(GO) build -o $(QUERY_BINARY) ./query

.PHONY: build-client-test
build-client-test:
	$(GO) build -o $(CLIENT_TEST_BINARY) ./client_test.go

# Build Docker image for server
.PHONY: docker-build
docker-build:
	$(DOCKER) build -t $(SERVER_IMAGE) .

# Run server in Docker
.PHONY: docker-run
docker-run: docker-build
	$(DOCKER) run --rm -p 50051:50051 -p 8080:8080 \
		-v $(PWD)/$(CONFIG):/app/$(CONFIG) \
		$(SERVER_IMAGE)

# Run server locally
.PHONY: run-server
run-server: build-server
	./$(SERVER_BINARY) -config=$(CONFIG)

# Run CZDS processor
.PHONY: run-czds
run-czds: build-czds
	./$(CZDS_BINARY) -config=$(CONFIG)

# Run query processor
.PHONY: run-query
run-query: build-query
	./$(QUERY_BINARY) -config=$(CONFIG)

# Run client test
.PHONY: run-client-test
run-client-test: build-client-test
	./$(CLIENT_TEST_BINARY)

# Run UI
.PHONY: run-ui
run-ui:
	cd $(UI_DIR) && $(NPM) install && $(NPM) start

# Clean build artifacts
.PHONY: clean
clean:
	rm -rf $(BINARY_DIR)
	$(DOCKER) image rm $(SERVER_IMAGE) || true
	cd $(UI_DIR) && rm -rf node_modules package-lock.json

# Install dependencies
.PHONY: deps
deps:
	$(GO) mod tidy
	$(GO) get github.com/grpc-ecosystem/grpc-gateway/v2
	$(GO) get github.com/lib/pq
	$(GO) get gopkg.in/yaml.v3
	$(GO) get github.com/google/uuid
	$(GO) get github.com/cenkalti/backoff/v4
	$(GO) get github.com/miekg/dns
	$(GO) get golang.org/x/net/publicsuffix
	$(GO) get github.com/rs/cors
	cd $(UI_DIR) && $(NPM) install