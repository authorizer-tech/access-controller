PWD = $(shell pwd)

TOOLS_BIN_DIR ?= $(PWD)/tmp/bin

MOCKGEN_BINARY = $(TOOLS_BIN_DIR)/mockgen
STATICCHECK_BINARY = $(TOOLS_BIN_DIR)/staticcheck
BUF_BINARY = $(TOOLS_BIN_DIR)/buf
PROTOC_GEN_BUF_LINT_BINARY = $(TOOLS_BIN_DIR)/protoc-gen-buf-lint
PROTOC_GEN_BUF_BREAKING_BINARY = $(TOOLS_BIN_DIR)/protoc-gen-buf-breaking
PROTOC_GEN_GRPC_GATEWAY_BINARY =  $(TOOLS_BIN_DIR)/protoc-gen-grpc-gateway
PROTOC_GEN_OPENAPIV2_BINARY =  $(TOOLS_BIN_DIR)/protoc-gen-openapiv2
PROTOC_GEN_GO_GRPC_BINARY = $(TOOLS_BIN_DIR)/protoc-gen-go-grpc
PROTOC_GEN_GO_BINARY = $(TOOLS_BIN_DIR)/protoc-gen-go

TOOLING=$(MOCKGEN_BINARY) $(STATICCHECK_BINARY) $(BUF_BINARY) $(PROTOC_GEN_BUF_LINT_BINARY) $(PROTOC_GEN_BUF_BREAKING_BINARY) $(PROTOC_GEN_GRPC_GATEWAY_BINARY) $(PROTOC_GEN_OPENAPIV2_BINARY) $(PROTOC_GEN_GO_GRPC_BINARY) $(PROTOC_GEN_GO_BINARY)

$(TOOLS_BIN_DIR):
	mkdir -p $(TOOLS_BIN_DIR)

$(TOOLING): $(TOOLS_BIN_DIR)
	@echo Installing tools from tools.go
	@cat tools.go | grep _ | awk -F'"' '{print $$2}' | GOBIN=$(TOOLS_BIN_DIR) xargs -tI % go install %

MOCK_CLIENT_ROUTER = $(PWD)/internal/mock_clientrouter_test.go
MOCK_HASHRING = $(PWD)/internal/mock_hashring_test.go
MOCK_NAMESPACE_MANAGER = $(PWD)/internal/mock_namespace_manager_test.go
MOCK_RELATION_STORE = $(PWD)/internal/mock_relationstore_test.go
MOCKS=$(MOCK_CLIENT_ROUTER) $(MOCK_HASHRING) $(MOCK_NAMESPACE_MANAGER) $(MOCK_RELATION_STORE)

$(MOCK_CLIENT_ROUTER): $(MOCKGEN_BINARY) $(PWD)/internal/client-router.go
	go generate $(PWD)/internal/client-router.go

$(MOCK_HASHRING): $(MOCKGEN_BINARY) $(PWD)/internal/hashring.go
	go generate $(PWD)/internal/hashring.go

$(MOCK_NAMESPACE_MANAGER): $(MOCK_BINARY) $(PWD)/internal/namespace.go
	go generate $(PWD)/internal/namespace.go

$(MOCK_RELATION_STORE): $(MOCK_BINARY) $(PWD)/internal/relation-store.go
	go generate $(PWD)/internal/relation-store.go

.PHONY: download
download:
	@go mod download

.PHONY: install-tools
install-tools: $(TOOLING)

.PHONY: mocks
mocks: $(MOCKS)

.PHONY: generate
generate: buf-generate  go-generate

.PHONY: go-generate
go-generate: install-tools mocks

.PHONY: buf-generate
buf-generate: install-tools
	$(BUF_BINARY) generate

.PHONY: test
test: generate
	@go test -v -race ./...

.PHONY: test-short
test-short: generate
	@go test -v -race -short ./...

.PHONY: test-coverage
test-coverage: generate
	@go test -v -race -coverprofile=coverage.out ./...

.PHONY: check
check: check-vet check-lint

.PHONY: check-vet
check-vet:
	@go vet ./...

.PHONY: check-lint
check-lint:
	$(STATICCHECK_BINARY) ./...

ci-test-coverage:
	@go test -v -race -coverprofile=coverage.txt -covermode=atomic ./...