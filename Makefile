# Makefile for nomad-tui
# Description: Build and deploy the nomad-tui TUI application

# Binary name
BINARY := nomad-tui

# Build variables
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS := -ldflags "-X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)"

# Go settings
GOCMD := go
GOBUILD := $(GOCMD) build
GOFMT := $(GOCMD) fmt
GOVET := $(GOCMD) vet
GOTEST := $(GOCMD) test
GOMOD := $(GOCMD) mod

.PHONY: all build clean fmt vet test tidy deploy help

# Default target
all: fmt vet build

# Build the binary
build:
	@echo "Building $(BINARY)..."
	$(GOBUILD) $(LDFLAGS) -o $(BINARY) .
	@echo "Build complete: ./$(BINARY)"

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -f $(BINARY)
	@echo "Clean complete"

# Format Go code
fmt:
	@echo "Formatting code..."
	$(GOFMT) ./...

# Run go vet
vet:
	@echo "Running vet..."
	$(GOVET) ./...

# Run tests
test:
	@echo "Running tests..."
	$(GOTEST) -v ./...

# Tidy dependencies
tidy:
	@echo "Tidying dependencies..."
	$(GOMOD) tidy

# Deploy the binary to a specified location
deploy: build
	@if [ -z "$(DEPLOY_PATH)" ]; then \
		echo ""; \
		echo "Where would you like to install $(BINARY)?"; \
		echo ""; \
		echo "  1) /usr/local/bin (system-wide, requires sudo)"; \
		echo "  2) ~/.local/bin (user-local)"; \
		echo "  3) ~/bin (user home)"; \
		echo "  4) Custom path"; \
		echo ""; \
		read -p "Select option [1-4]: " choice; \
		case "$$choice" in \
			1) dest="/usr/local/bin"; use_sudo="yes" ;; \
			2) dest="$$HOME/.local/bin"; use_sudo="no" ;; \
			3) dest="$$HOME/bin"; use_sudo="no" ;; \
			4) read -p "Enter custom path: " dest; use_sudo="no" ;; \
			*) echo "Invalid option"; exit 1 ;; \
		esac; \
		if [ ! -d "$$dest" ]; then \
			echo "Creating directory: $$dest"; \
			mkdir -p "$$dest" || { echo "Failed to create directory"; exit 1; }; \
		fi; \
		echo "Installing $(BINARY) to $$dest..."; \
		if [ "$$use_sudo" = "yes" ]; then \
			sudo cp $(BINARY) "$$dest/$(BINARY)" && sudo chmod 755 "$$dest/$(BINARY)"; \
		else \
			cp $(BINARY) "$$dest/$(BINARY)" && chmod 755 "$$dest/$(BINARY)"; \
		fi; \
		echo ""; \
		echo "Installed: $$dest/$(BINARY)"; \
	else \
		dest="$(DEPLOY_PATH)"; \
		if [ ! -d "$$dest" ]; then \
			echo "Creating directory: $$dest"; \
			mkdir -p "$$dest" || { echo "Failed to create directory"; exit 1; }; \
		fi; \
		echo "Installing $(BINARY) to $$dest..."; \
		cp $(BINARY) "$$dest/$(BINARY)" && chmod 755 "$$dest/$(BINARY)"; \
		echo "Installed: $$dest/$(BINARY)"; \
	fi

# Help target
help:
	@echo "Usage: make [TARGET]"
	@echo ""
	@echo "Targets:"
	@echo "  all      Format, vet, and build (default)"
	@echo "  build    Build the $(BINARY) binary"
	@echo "  clean    Remove build artifacts"
	@echo "  fmt      Format Go source code"
	@echo "  vet      Run go vet for static analysis"
	@echo "  test     Run tests"
	@echo "  tidy     Tidy go module dependencies"
	@echo "  deploy   Build and install binary (interactive prompt)"
	@echo "  help     Show this help message"
	@echo ""
	@echo "Environment variables:"
	@echo "  DEPLOY_PATH  Skip interactive prompt and deploy to specified path"
	@echo "               Example: make deploy DEPLOY_PATH=/opt/bin"
