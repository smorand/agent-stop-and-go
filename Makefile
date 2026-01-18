.PHONY: build build-all install install-launcher uninstall clean clean-all rebuild test fmt vet lint check run info help list-commands

# Detect current platform
GOOS=$(shell go env GOOS)
GOARCH=$(shell go env GOARCH)
CURRENT_PLATFORM=$(GOOS)-$(GOARCH)

# Detect install directory based on user privileges (root vs non-root)
IS_ROOT=$(shell [ $$(id -u) -eq 0 ] && echo "yes" || echo "no")
ifeq ($(IS_ROOT),yes)
	DEFAULT_INSTALL_DIR=/usr/local/bin
	DEFAULT_LIB_DIR=/usr/local/lib
	SUDO_CMD=
else
	DEFAULT_INSTALL_DIR=$(HOME)/.local/bin
	DEFAULT_LIB_DIR=$(HOME)/.local/lib
	SUDO_CMD=
endif

# Auto-detect project structure
HAS_SRC_DIR=$(shell [ -d src ] && echo "yes" || echo "no")
HAS_CMD_DIR=$(shell [ -d cmd ] && echo "yes" || echo "no")

# Detect all commands in cmd/ directory (if multi-command layout)
ifeq ($(HAS_CMD_DIR),yes)
	COMMANDS=$(shell find cmd -mindepth 1 -maxdepth 1 -type d -exec basename {} \;)
	HAS_MULTIPLE_CMDS=$(shell [ $$(find cmd -mindepth 1 -maxdepth 1 -type d | wc -l) -gt 1 ] && echo "yes" || echo "no")
else
	COMMANDS=
	HAS_MULTIPLE_CMDS=no
endif

# Default binary name (for single-command projects)
DEFAULT_BINARY_NAME=$(shell basename $$(pwd))

# Set directories based on project structure
ifeq ($(HAS_SRC_DIR),yes)
	SRC_DIR=src
	BUILD_DIR=bin
	GO_MOD_PATH=$(SRC_DIR)/go.mod
	GO_SUM_PATH=$(SRC_DIR)/go.sum
else
	SRC_DIR=.
	BUILD_DIR=bin
	GO_MOD_PATH=go.mod
	GO_SUM_PATH=go.sum
endif

# Build for current platform only
build:
ifeq ($(HAS_MULTIPLE_CMDS),yes)
	@echo "Building all commands for current platform ($(CURRENT_PLATFORM))..."
	@$(foreach cmd,$(COMMANDS),$(MAKE) build-cmd-current CMD=$(cmd);)
else ifeq ($(HAS_CMD_DIR),yes)
	@$(MAKE) build-cmd-current CMD=$(DEFAULT_BINARY_NAME)
else ifeq ($(HAS_SRC_DIR),yes)
	@$(MAKE) build-single-current
else
	@$(MAKE) build-flat-current
endif

# Build for all platforms and create launcher scripts
build-all:
ifeq ($(HAS_MULTIPLE_CMDS),yes)
	@echo "Building all commands for all platforms..."
	@$(foreach cmd,$(COMMANDS),$(MAKE) build-cmd-all CMD=$(cmd);)
else ifeq ($(HAS_CMD_DIR),yes)
	@$(MAKE) build-cmd-all CMD=$(DEFAULT_BINARY_NAME)
else ifeq ($(HAS_SRC_DIR),yes)
	@$(MAKE) build-single-all
else
	@$(MAKE) build-flat-all
endif

rebuild: clean-all build

# Helper target: Build single command for current platform (multi-command layout)
build-cmd-current: $(GO_SUM_PATH)
	@echo "Building $(CMD) for $(CURRENT_PLATFORM)..."
	@mkdir -p $(BUILD_DIR)
	@GOOS=$(GOOS) GOARCH=$(GOARCH) go build -o $(BUILD_DIR)/$(CMD)-$(CURRENT_PLATFORM) ./cmd/$(CMD)
	@echo "✓ Built: $(BUILD_DIR)/$(CMD)-$(CURRENT_PLATFORM)"

# Helper target: Build single command for all platforms (multi-command layout)
build-cmd-all: $(GO_SUM_PATH)
	@echo "Building $(CMD) for all platforms..."
	@mkdir -p $(BUILD_DIR)
	@GOOS=linux GOARCH=amd64 go build -o $(BUILD_DIR)/$(CMD)-linux-amd64 ./cmd/$(CMD)
	@echo "✓ Built: $(BUILD_DIR)/$(CMD)-linux-amd64"
	@GOOS=darwin GOARCH=amd64 go build -o $(BUILD_DIR)/$(CMD)-darwin-amd64 ./cmd/$(CMD)
	@echo "✓ Built: $(BUILD_DIR)/$(CMD)-darwin-amd64"
	@GOOS=darwin GOARCH=arm64 go build -o $(BUILD_DIR)/$(CMD)-darwin-arm64 ./cmd/$(CMD)
	@echo "✓ Built: $(BUILD_DIR)/$(CMD)-darwin-arm64"
	@$(MAKE) create-launcher BINARY_NAME=$(CMD)

# Helper target: Build for current platform (single-command with cmd/ layout)
build-single-cmd-current: $(GO_SUM_PATH)
	@echo "Building $(DEFAULT_BINARY_NAME) for $(CURRENT_PLATFORM)..."
	@mkdir -p $(BUILD_DIR)
	@GOOS=$(GOOS) GOARCH=$(GOARCH) go build -o $(BUILD_DIR)/$(DEFAULT_BINARY_NAME)-$(CURRENT_PLATFORM) ./cmd/$(DEFAULT_BINARY_NAME)
	@echo "✓ Built: $(BUILD_DIR)/$(DEFAULT_BINARY_NAME)-$(CURRENT_PLATFORM)"

# Helper target: Build for all platforms (single-command with cmd/ layout)
build-single-cmd-all: $(GO_SUM_PATH)
	@echo "Building $(DEFAULT_BINARY_NAME) for all platforms..."
	@mkdir -p $(BUILD_DIR)
	@GOOS=linux GOARCH=amd64 go build -o $(BUILD_DIR)/$(DEFAULT_BINARY_NAME)-linux-amd64 ./cmd/$(DEFAULT_BINARY_NAME)
	@echo "✓ Built: $(BUILD_DIR)/$(DEFAULT_BINARY_NAME)-linux-amd64"
	@GOOS=darwin GOARCH=amd64 go build -o $(BUILD_DIR)/$(DEFAULT_BINARY_NAME)-darwin-amd64 ./cmd/$(DEFAULT_BINARY_NAME)
	@echo "✓ Built: $(BUILD_DIR)/$(DEFAULT_BINARY_NAME)-darwin-amd64"
	@GOOS=darwin GOARCH=arm64 go build -o $(BUILD_DIR)/$(DEFAULT_BINARY_NAME)-darwin-arm64 ./cmd/$(DEFAULT_BINARY_NAME)
	@echo "✓ Built: $(BUILD_DIR)/$(DEFAULT_BINARY_NAME)-darwin-arm64"
	@$(MAKE) create-launcher BINARY_NAME=$(DEFAULT_BINARY_NAME)

# Helper target: Build for current platform (src/ layout)
build-single-current: $(GO_SUM_PATH)
	@echo "Building $(DEFAULT_BINARY_NAME) for $(CURRENT_PLATFORM)..."
	@mkdir -p $(BUILD_DIR)
	@cd $(SRC_DIR) && GOOS=$(GOOS) GOARCH=$(GOARCH) go build -o ../$(BUILD_DIR)/$(DEFAULT_BINARY_NAME)-$(CURRENT_PLATFORM) .
	@echo "✓ Built: $(BUILD_DIR)/$(DEFAULT_BINARY_NAME)-$(CURRENT_PLATFORM)"

# Helper target: Build for all platforms (src/ layout)
build-single-all: $(GO_SUM_PATH)
	@echo "Building $(DEFAULT_BINARY_NAME) for all platforms..."
	@mkdir -p $(BUILD_DIR)
	@cd $(SRC_DIR) && GOOS=linux GOARCH=amd64 go build -o ../$(BUILD_DIR)/$(DEFAULT_BINARY_NAME)-linux-amd64 .
	@echo "✓ Built: $(BUILD_DIR)/$(DEFAULT_BINARY_NAME)-linux-amd64"
	@cd $(SRC_DIR) && GOOS=darwin GOARCH=amd64 go build -o ../$(BUILD_DIR)/$(DEFAULT_BINARY_NAME)-darwin-amd64 .
	@echo "✓ Built: $(BUILD_DIR)/$(DEFAULT_BINARY_NAME)-darwin-amd64"
	@cd $(SRC_DIR) && GOOS=darwin GOARCH=arm64 go build -o ../$(BUILD_DIR)/$(DEFAULT_BINARY_NAME)-darwin-arm64 .
	@echo "✓ Built: $(BUILD_DIR)/$(DEFAULT_BINARY_NAME)-darwin-arm64"
	@$(MAKE) create-launcher BINARY_NAME=$(DEFAULT_BINARY_NAME)

# Helper target: Build for current platform (flat layout)
build-flat-current: $(GO_SUM_PATH)
	@echo "Building $(DEFAULT_BINARY_NAME) for $(CURRENT_PLATFORM)..."
	@mkdir -p $(BUILD_DIR)
	@GOOS=$(GOOS) GOARCH=$(GOARCH) go build -o $(BUILD_DIR)/$(DEFAULT_BINARY_NAME)-$(CURRENT_PLATFORM) .
	@echo "✓ Built: $(BUILD_DIR)/$(DEFAULT_BINARY_NAME)-$(CURRENT_PLATFORM)"

# Helper target: Build for all platforms (flat layout)
build-flat-all: $(GO_SUM_PATH)
	@echo "Building $(DEFAULT_BINARY_NAME) for all platforms..."
	@mkdir -p $(BUILD_DIR)
	@GOOS=linux GOARCH=amd64 go build -o $(BUILD_DIR)/$(DEFAULT_BINARY_NAME)-linux-amd64 .
	@echo "✓ Built: $(BUILD_DIR)/$(DEFAULT_BINARY_NAME)-linux-amd64"
	@GOOS=darwin GOARCH=amd64 go build -o $(BUILD_DIR)/$(DEFAULT_BINARY_NAME)-darwin-amd64 .
	@echo "✓ Built: $(BUILD_DIR)/$(DEFAULT_BINARY_NAME)-darwin-amd64"
	@GOOS=darwin GOARCH=arm64 go build -o $(BUILD_DIR)/$(DEFAULT_BINARY_NAME)-darwin-arm64 .
	@echo "✓ Built: $(BUILD_DIR)/$(DEFAULT_BINARY_NAME)-darwin-arm64"
	@$(MAKE) create-launcher BINARY_NAME=$(DEFAULT_BINARY_NAME)

# Create launcher script for a specific binary
create-launcher:
	@echo "Creating launcher script for $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	@echo '#!/bin/bash' > $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '# Auto-generated launcher script for $(BINARY_NAME)' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '# Detects platform and executes the correct binary' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '# Get the directory where this script is located' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo 'SCRIPT_DIR="$$(cd "$$(dirname "$${BASH_SOURCE[0]}")" && pwd)"' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '# Detect OS' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo 'OS=$$(uname -s | tr "[:upper:]" "[:lower:]")' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '# Detect architecture' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo 'ARCH=$$(uname -m)' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '# Map architecture names to Go convention' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo 'case "$$ARCH" in' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '    x86_64)' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '        ARCH="amd64"' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '        ;;' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '    aarch64)' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '        ARCH="arm64"' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '        ;;' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '    arm64)' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '        ARCH="arm64"' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '        ;;' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '    *)' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '        echo "Unsupported architecture: $$ARCH" >&2' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '        exit 1' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '        ;;' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo 'esac' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '# Construct binary name' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo 'BINARY="$$SCRIPT_DIR/$(BINARY_NAME)-$$OS-$$ARCH"' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '# Check if binary exists' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo 'if [ ! -f "$$BINARY" ]; then' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '    echo "Error: Binary not found for platform $$OS-$$ARCH" >&2' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '    echo "Expected: $$BINARY" >&2' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '    echo "" >&2' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '    echo "Available binaries:" >&2' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '    ls -1 "$$SCRIPT_DIR"/$(BINARY_NAME)-* 2>/dev/null | sed "s|^|  |" >&2' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '    exit 1' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo 'fi' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo '# Execute the binary with all arguments' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo 'exec "$$BINARY" "$$@"' >> $(BUILD_DIR)/$(BINARY_NAME).sh
	@chmod +x $(BUILD_DIR)/$(BINARY_NAME).sh
	@echo "✓ Created launcher script: $(BUILD_DIR)/$(BINARY_NAME).sh"

# Generate go.sum
$(GO_SUM_PATH): $(GO_MOD_PATH)
	@echo "Downloading dependencies..."
ifeq ($(HAS_SRC_DIR),yes)
	@cd $(SRC_DIR) && go mod download
	@cd $(SRC_DIR) && go mod tidy
	@touch $(GO_SUM_PATH)
else
	@go mod download
	@go mod tidy
	@touch $(GO_SUM_PATH)
endif
	@echo "Dependencies downloaded"

# Generate go.mod (only if it doesn't exist)
$(GO_MOD_PATH):
	@echo "Initializing Go module..."
ifeq ($(HAS_SRC_DIR),yes)
	@cd $(SRC_DIR) && go mod init $(DEFAULT_BINARY_NAME)
else
	@go mod init $(DEFAULT_BINARY_NAME)
endif

# Install binary (installs current platform binaries)
install: build
ifeq ($(HAS_MULTIPLE_CMDS),yes)
	@echo "Installing all commands for current platform ($(CURRENT_PLATFORM))..."
ifndef TARGET
	@mkdir -p $(DEFAULT_INSTALL_DIR)
	@$(foreach cmd,$(COMMANDS), \
		if [ -f "$(BUILD_DIR)/$(cmd)-$(CURRENT_PLATFORM)" ]; then \
			echo "Installing $(cmd) to $(DEFAULT_INSTALL_DIR)..."; \
			cp $(BUILD_DIR)/$(cmd)-$(CURRENT_PLATFORM) $(DEFAULT_INSTALL_DIR)/$(cmd); \
		fi;)
else
	@$(foreach cmd,$(COMMANDS), \
		if [ -f "$(BUILD_DIR)/$(cmd)-$(CURRENT_PLATFORM)" ]; then \
			echo "Installing $(cmd) to $(TARGET)..."; \
			cp $(BUILD_DIR)/$(cmd)-$(CURRENT_PLATFORM) $(TARGET)/$(cmd); \
		fi;)
endif
	@echo "Installation complete!"
else
ifndef TARGET
	@mkdir -p $(DEFAULT_INSTALL_DIR)
	@echo "Installing $(DEFAULT_BINARY_NAME) ($(CURRENT_PLATFORM)) to $(DEFAULT_INSTALL_DIR)..."
	@cp $(BUILD_DIR)/$(DEFAULT_BINARY_NAME)-$(CURRENT_PLATFORM) $(DEFAULT_INSTALL_DIR)/$(DEFAULT_BINARY_NAME)
	@echo "Installation complete!"
else
	@echo "Installing $(DEFAULT_BINARY_NAME) ($(CURRENT_PLATFORM)) to $(TARGET)..."
	@cp $(BUILD_DIR)/$(DEFAULT_BINARY_NAME)-$(CURRENT_PLATFORM) $(TARGET)/$(DEFAULT_BINARY_NAME)
	@echo "Installation complete!"
endif
endif

# Install launcher scripts (for multi-platform distribution)
install-launcher: build-all
ifeq ($(HAS_MULTIPLE_CMDS),yes)
	@echo "Installing launcher scripts for all commands..."
ifndef TARGET
	@mkdir -p $(DEFAULT_INSTALL_DIR)
	@$(foreach cmd,$(COMMANDS), \
		echo "Installing launcher for $(cmd) to $(DEFAULT_INSTALL_DIR)..."; \
		cp $(BUILD_DIR)/$(cmd).sh $(DEFAULT_INSTALL_DIR)/$(cmd); \
		mkdir -p $(DEFAULT_LIB_DIR)/$(cmd); \
		cp $(BUILD_DIR)/$(cmd)-linux-amd64 $(DEFAULT_LIB_DIR)/$(cmd)/ 2>/dev/null || true; \
		cp $(BUILD_DIR)/$(cmd)-darwin-amd64 $(DEFAULT_LIB_DIR)/$(cmd)/ 2>/dev/null || true; \
		cp $(BUILD_DIR)/$(cmd)-darwin-arm64 $(DEFAULT_LIB_DIR)/$(cmd)/ 2>/dev/null || true;)
else
	@$(foreach cmd,$(COMMANDS), \
		echo "Installing launcher for $(cmd) to $(TARGET)..."; \
		cp $(BUILD_DIR)/$(cmd).sh $(TARGET)/$(cmd);)
	@echo "Note: Platform binaries remain in $(BUILD_DIR)/"
endif
	@echo "Installation complete!"
else
ifndef TARGET
	@mkdir -p $(DEFAULT_INSTALL_DIR)
	@echo "Installing launcher script to $(DEFAULT_INSTALL_DIR)/$(DEFAULT_BINARY_NAME)..."
	@cp $(BUILD_DIR)/$(DEFAULT_BINARY_NAME).sh $(DEFAULT_INSTALL_DIR)/$(DEFAULT_BINARY_NAME)
	@echo "Installing platform binaries to $(DEFAULT_LIB_DIR)/$(DEFAULT_BINARY_NAME)/..."
	@mkdir -p $(DEFAULT_LIB_DIR)/$(DEFAULT_BINARY_NAME)
	@cp $(BUILD_DIR)/$(DEFAULT_BINARY_NAME)-linux-amd64 $(DEFAULT_LIB_DIR)/$(DEFAULT_BINARY_NAME)/
	@cp $(BUILD_DIR)/$(DEFAULT_BINARY_NAME)-darwin-amd64 $(DEFAULT_LIB_DIR)/$(DEFAULT_BINARY_NAME)/
	@cp $(BUILD_DIR)/$(DEFAULT_BINARY_NAME)-darwin-arm64 $(DEFAULT_LIB_DIR)/$(DEFAULT_BINARY_NAME)/
	@echo "Installation complete!"
else
	@echo "Installing launcher script to $(TARGET)/$(DEFAULT_BINARY_NAME)..."
	@cp $(BUILD_DIR)/$(DEFAULT_BINARY_NAME).sh $(TARGET)/$(DEFAULT_BINARY_NAME)
	@echo "Note: Platform binaries remain in $(BUILD_DIR)/"
	@echo "Installation complete!"
endif
endif

# Uninstall binaries
uninstall:
ifeq ($(HAS_MULTIPLE_CMDS),yes)
	@echo "Uninstalling all commands..."
	@$(foreach cmd,$(COMMANDS), \
		BINARY_PATH=$$(which $(cmd) 2>/dev/null); \
		if [ -n "$$BINARY_PATH" ]; then \
			echo "Removing $(cmd) from $$BINARY_PATH..."; \
			rm -f "$$BINARY_PATH" 2>/dev/null || sudo rm -f "$$BINARY_PATH"; \
			if [ -d "/usr/local/lib/$(cmd)" ]; then \
				echo "Removing platform binaries for $(cmd) from /usr/local/lib..."; \
				sudo rm -rf "/usr/local/lib/$(cmd)"; \
			fi; \
			if [ -d "$(HOME)/.local/lib/$(cmd)" ]; then \
				echo "Removing platform binaries for $(cmd) from ~/.local/lib..."; \
				rm -rf "$(HOME)/.local/lib/$(cmd)"; \
			fi; \
		fi;)
	@echo "Uninstallation complete!"
else
	@echo "Looking for $(DEFAULT_BINARY_NAME) in system..."
	@BINARY_PATH=$$(which $(DEFAULT_BINARY_NAME) 2>/dev/null); \
	if [ -z "$$BINARY_PATH" ]; then \
		echo "$(DEFAULT_BINARY_NAME) not found in PATH"; \
		exit 0; \
	fi; \
	if [ -f "$$BINARY_PATH" ]; then \
		if [ "$$(basename $$(dirname $$BINARY_PATH))" = "bin" ]; then \
			echo "Found $(DEFAULT_BINARY_NAME) at $$BINARY_PATH"; \
			echo "Removing..."; \
			rm -f "$$BINARY_PATH" 2>/dev/null || sudo rm -f "$$BINARY_PATH"; \
			if [ -d "/usr/local/lib/$(DEFAULT_BINARY_NAME)" ]; then \
				echo "Removing platform binaries from /usr/local/lib..."; \
				sudo rm -rf "/usr/local/lib/$(DEFAULT_BINARY_NAME)"; \
			fi; \
			if [ -d "$(HOME)/.local/lib/$(DEFAULT_BINARY_NAME)" ]; then \
				echo "Removing platform binaries from ~/.local/lib..."; \
				rm -rf "$(HOME)/.local/lib/$(DEFAULT_BINARY_NAME)"; \
			fi; \
			echo "Uninstallation complete!"; \
		else \
			echo "$(DEFAULT_BINARY_NAME) found at $$BINARY_PATH but not in a standard bin directory"; \
			echo "Please remove it manually if needed"; \
		fi; \
	fi
endif

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf $(BUILD_DIR)
	@rm -f ./$(DEFAULT_BINARY_NAME)
	@echo "Clean complete!"

# Clean all (including go.mod and go.sum)
clean-all: clean
	@echo "Cleaning go.mod & go.sum..."
	@rm -f $(GO_MOD_PATH) $(GO_SUM_PATH)
	@echo "Clean complete!"

# Run tests
test:
	@echo "Running tests..."
ifeq ($(HAS_SRC_DIR),yes)
	@cd $(SRC_DIR) && go test -v ./...
else
	@go test -v ./...
endif

# Format code
fmt:
	@echo "Formatting code..."
ifeq ($(HAS_SRC_DIR),yes)
	@cd $(SRC_DIR) && go fmt ./...
else
	@go fmt ./...
endif
	@echo "Format complete!"

# Run go vet
vet:
	@echo "Running go vet..."
ifeq ($(HAS_SRC_DIR),yes)
	@cd $(SRC_DIR) && go vet ./...
else
	@go vet ./...
endif
	@echo "Vet complete!"

# Run linter (golangci-lint if available, otherwise fallback to vet)
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		echo "Running golangci-lint..."; \
		golangci-lint run ./...; \
		echo "Lint complete!"; \
	else \
		echo "golangci-lint not found, falling back to go vet..."; \
		echo "Install golangci-lint: https://golangci-lint.run/welcome/install/"; \
		$(MAKE) vet; \
	fi

# Run all checks (fmt, vet, lint, test)
check: fmt vet lint test
	@echo "All checks passed!"

# Run the application (passes arguments via ARGS and CMD variables)
run: build
ifeq ($(HAS_MULTIPLE_CMDS),yes)
ifndef CMD
	@echo "Error: Multi-command project detected. Please specify CMD variable."
	@echo "Example: make run CMD=mycommand ARGS='--help'"
	@echo "Available commands:"
	@$(foreach cmd,$(COMMANDS),echo "  - $(cmd)";)
	@exit 1
else
	@echo "Running $(CMD)..."
	@$(BUILD_DIR)/$(CMD)-$(CURRENT_PLATFORM) $(ARGS)
endif
else
	@echo "Running $(DEFAULT_BINARY_NAME)..."
	@$(BUILD_DIR)/$(DEFAULT_BINARY_NAME)-$(CURRENT_PLATFORM) $(ARGS)
endif

# List all available commands (for multi-command projects)
list-commands:
ifeq ($(HAS_MULTIPLE_CMDS),yes)
	@echo "Available commands in this project:"
	@$(foreach cmd,$(COMMANDS),echo "  - $(cmd)";)
else
	@echo "Single-command project: $(DEFAULT_BINARY_NAME)"
endif

# Show current platform info
info:
	@echo "Current platform: $(CURRENT_PLATFORM)"
	@echo "Build directory: $(BUILD_DIR)"
	@echo "Project structure: $(SRC_DIR)"
ifeq ($(HAS_MULTIPLE_CMDS),yes)
	@echo "Multi-command project: yes"
	@echo "Commands: $(COMMANDS)"
else
	@echo "Binary name: $(DEFAULT_BINARY_NAME)"
endif

# Help
help:
	@echo "Available targets:"
	@echo "  build            - Build binaries for current platform ($(CURRENT_PLATFORM))"
	@echo "  build-all        - Build for all platforms and create launcher scripts"
	@echo "  run              - Build and run the binary"
	@echo "  rebuild          - Clean all and rebuild from scratch"
	@echo "  install          - Install current platform binaries (root: /usr/local/bin, user: ~/.local/bin, or TARGET)"
	@echo "  install-launcher - Install launcher scripts with all platform binaries"
	@echo "  uninstall        - Remove installed binaries"
	@echo "  clean            - Remove build artifacts"
	@echo "  clean-all        - Remove build artifacts, go.mod, and go.sum"
	@echo "  test             - Run tests"
	@echo "  fmt              - Format code"
	@echo "  vet              - Run go vet"
	@echo "  lint             - Run golangci-lint (or go vet if not installed)"
	@echo "  check            - Run fmt, vet, lint, and test"
	@echo "  list-commands    - List all available commands (multi-command projects)"
	@echo "  info             - Show current platform and project information"
	@echo "  help             - Show this help message"
	@echo ""
ifeq ($(HAS_MULTIPLE_CMDS),yes)
	@echo "Multi-command project detected. Available commands:"
	@$(foreach cmd,$(COMMANDS),echo "  - $(cmd)";)
	@echo ""
	@echo "Examples:"
	@echo "  make build                     - Build all commands for current platform"
	@echo "  make build-all                 - Build all commands for all platforms"
	@echo "  make run CMD=mycommand         - Run specific command"
	@echo "  make run CMD=mycommand ARGS='--help' - Run with arguments"
	@echo "  make install                   - Install all commands for current platform"
	@echo "  make install-launcher          - Install launcher scripts for all commands"
else
	@echo "Examples:"
	@echo "  make run                       - Run with no arguments"
	@echo "  make run ARGS='--help'         - Run with --help flag"
	@echo "  make run ARGS='arg1 arg2'      - Run with multiple arguments"
endif
	@echo ""
	@echo "Platform-specific binaries are created in $(BUILD_DIR)/ with suffixes:"
	@echo "  -linux-amd64   - Linux (Intel/AMD 64-bit)"
	@echo "  -darwin-amd64  - macOS (Intel)"
	@echo "  -darwin-arm64  - macOS (Apple Silicon)"
	@echo ""
	@echo "Launcher scripts (.sh) automatically detect platform and execute the right binary."
