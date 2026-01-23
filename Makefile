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

.PHONY: all build clean fmt vet test tidy deploy release help

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

# Interactive release workflow
release:
	@echo "=== Release Workflow ==="
	@echo ""
	@latest=$$(git describe --tags --abbrev=0 2>/dev/null || echo "No tags yet"); \
	echo "Current latest tag: $$latest"; \
	echo ""; \
	if git status --porcelain | grep -q .; then \
		echo "WARNING: You have uncommitted changes:"; \
		git status --short; \
		echo ""; \
		read -p "Do you want to commit these changes first? [y/N]: " commit_choice; \
		if [ "$$commit_choice" = "y" ] || [ "$$commit_choice" = "Y" ]; then \
			echo "Please commit your changes and run 'make release' again."; \
			exit 1; \
		fi; \
	fi; \
	echo ""; \
	read -p "Enter new version tag (e.g., v1.0.0): " new_tag; \
	if [ -z "$$new_tag" ]; then \
		echo "No tag provided. Exiting."; \
		exit 1; \
	fi; \
	if ! echo "$$new_tag" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+'; then \
		echo "WARNING: Tag should follow semantic versioning (e.g., v1.0.0)"; \
		read -p "Continue anyway? [y/N]: " continue; \
		if [ "$$continue" != "y" ] && [ "$$continue" != "Y" ]; then \
			echo "Cancelled."; \
			exit 1; \
		fi; \
	fi; \
	if git rev-parse "$$new_tag" >/dev/null 2>&1; then \
		echo "ERROR: Tag $$new_tag already exists!"; \
		exit 1; \
	fi; \
	echo ""; \
	echo "Summary:"; \
	echo "  Previous tag: $$latest"; \
	echo "  New tag:      $$new_tag"; \
	echo ""; \
	echo "This will:"; \
	echo "  1. Create tag $$new_tag"; \
	echo "  2. Push tag to origin"; \
	echo "  3. Trigger GitHub Actions to build and release"; \
	echo ""; \
	read -p "Proceed with release? [y/N]: " confirm; \
	if [ "$$confirm" = "y" ] || [ "$$confirm" = "Y" ]; then \
		echo "Creating tag $$new_tag..."; \
		git tag -a "$$new_tag" -m "Release $$new_tag" || exit 1; \
		echo "Pushing tag to origin..."; \
		git push origin "$$new_tag" || exit 1; \
		echo ""; \
		echo "✓ Release $$new_tag created and pushed!"; \
		echo ""; \
		echo "GitHub Actions will now build the release."; \
		echo "Check: https://github.com/osage-io/nomad-tui/actions"; \
	else \
		echo "Release cancelled."; \
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
	@echo "  release  Interactive release workflow (tag and push)"
	@echo "  help     Show this help message"
	@echo ""
	@echo "Environment variables:"
	@echo "  DEPLOY_PATH  Skip interactive prompt and deploy to specified path"
	@echo "               Example: make deploy DEPLOY_PATH=/opt/bin"
