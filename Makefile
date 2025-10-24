.PHONY: build install clean test run dev

# Build variables
BINARY_NAME=repoxy
VERSION?=1.0.0
BUILD_DIR=.
GO=go
INSTALL_PATH=/usr/local/bin

# Build the binary (native)
build:
	$(GO) build -ldflags="-s -w" -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/repoxy

# Build for Linux AMD64
build-linux-amd64:
	GOOS=linux GOARCH=amd64 $(GO) build -ldflags="-s -w" -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/repoxy

# Build for Linux ARM64
build-linux-arm64:
	GOOS=linux GOARCH=arm64 $(GO) build -ldflags="-s -w" -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 ./cmd/repoxy

# Build for all Linux platforms (parallel)
build-linux: build-linux-amd64 build-linux-arm64

# Install the binary
install: build
	sudo cp $(BUILD_DIR)/$(BINARY_NAME) $(INSTALL_PATH)/
	sudo chmod +x $(INSTALL_PATH)/$(BINARY_NAME)
	@echo "Installed to $(INSTALL_PATH)/$(BINARY_NAME)"

# Install systemd service
install-service:
	sudo mkdir -p /var/cache/repoxy
	sudo useradd -r -s /bin/false -d /var/cache/repoxy repoxy || true
	sudo chown -R repoxy:repoxy /var/cache/repoxy
	sudo cp repoxy.service /etc/systemd/system/
	sudo systemctl daemon-reload
	@echo "Service installed. Enable with: sudo systemctl enable repoxy"

# Uninstall
uninstall:
	sudo systemctl stop repoxy || true
	sudo systemctl disable repoxy || true
	sudo rm -f /etc/systemd/system/repoxy.service
	sudo systemctl daemon-reload
	sudo rm -f $(INSTALL_PATH)/$(BINARY_NAME)

# Clean build artifacts
clean:
	rm -f $(BUILD_DIR)/$(BINARY_NAME)
	go clean

# Run tests
test:
	$(GO) test -v ./...

# Run with default config
run: build
	./$(BINARY_NAME) -config config.yaml

# Run for development (with verbose output)
dev: build
	./$(BINARY_NAME) -config config.yaml

# Vet the code
vet:
	$(GO) vet ./...

# Format the code
fmt:
	$(GO) fmt ./...

# Show help
help:
	@echo "Available targets:"
	@echo "  build              - Build for current platform"
	@echo "  build-linux-amd64  - Build for Linux AMD64"
	@echo "  build-linux-arm64  - Build for Linux ARM64"
	@echo "  build-linux        - Build for all Linux platforms"
	@echo "  install            - Install binary"
	@echo "  install-service    - Install systemd service"
	@echo "  uninstall          - Uninstall binary and service"
	@echo "  clean              - Clean build artifacts"
	@echo "  test               - Run tests"
	@echo "  run                - Run with config.yaml"
	@echo "  dev                - Run in dev mode"
	@echo "  vet                - Run go vet"
	@echo "  fmt                - Format code"
