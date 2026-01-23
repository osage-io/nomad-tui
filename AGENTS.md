# AGENTS.md

This file contains instructions for agentic coding tools operating in this repository. It provides build/lint/test commands and code style guidelines to ensure consistency across contributions.

## Build/Lint/Test Commands

### Shell Scripts (Primary Language)
```bash
# Lint all shell scripts
find . -name "*.sh" -exec /usr/local/bin/shellcheck {} \;

# Run specific script through shellcheck
/usr/local/bin/shellcheck path/to/script.sh

# Test shell scripts with bats (if available)
bats tests/

# Run single test file
bats tests/specific_test.bats

# Execute script with debugging
bash -x script.sh

# Check syntax without execution
bash -n script.sh
```

### Go (Nomad Top Tool)
```bash
# Format Go code
go fmt .

# Lint Go code
go vet .

# Build the project
go build -o nomad-tui .

# Run tests (if any)
go test ./...
```

### General Development
```bash
# Format all shell scripts
shfmt -w -i 2 -ci **/*.sh

# Check for common issues
find . -name "*.sh" -exec grep -l "set -e" {} \; | xargs -I {} sh -c 'echo "Checking {}"; /usr/local/bin/shellcheck --exclude=SC2155 {}'

# Validate JSON/YAML configs
find . -name "*.json" -exec jq . {} \; >/dev/null
find . -name "*.yaml" -o -name "*.yml" -exec yamllint {} \;
```

### Testing Strategy
- Use `bats` for shell script testing
- Write tests in `tests/` directory with `.bats` extension
- Test file naming: `test_<feature>.bats`
- Run single test: `bats tests/test_nomad_install.bats`

## Nomad Top Tool

The `nomad-tui` is a terminal user interface (TUI) for monitoring and managing Nomad clusters, built with Go and Bubbletea.

### Features
- View Nomad jobs, nodes, and cluster overview
- Jobs sorted with running jobs displayed first
- Job selection with arrow keys for navigation
- Stop and delete selected jobs
- Display dynamically limited to terminal height, with headers always visible
- Support for connecting to different Nomad servers via `-addr` flag
- TLS certificate verification skip with `-skip-verify` flag
- ACL token authentication with `-token` flag
- Color-coded status indicators (green for running, yellow for pending, red for failed)
- Bold job names and headers
- Interactive help screen with colorized instructions
- Support for color themes via `-theme` flag (default, dracula)

### Building and Running
```bash
go build -o nomad-tui .
./nomad-tui [-addr <server>] [-token <token>] [-skip-verify] [-theme <theme>]
```

### Controls
- `q`: Quit the application
- `r`: Refresh data
- `j`: Switch to jobs view
- `n`: Switch to nodes view
- `c`: Switch to cluster view
- `↑/↓`: Select job (in jobs view)
- `s`: Stop selected job
- `d`: Delete selected job
- `h`: Toggle help screen

### Terminal Compatibility
- Uses alternate screen mode for full terminal utilization
- Headers positioned on line 2 to ensure visibility in terminals with title bars
- Responsive to window resizing, truncating content to fit height

## Code Style Guidelines

### Shell Scripting

#### File Structure
- Use `.sh` extension for all shell scripts
- Start with shebang: `#!/usr/bin/env bash`
- Set shell options: `set -euo pipefail`
- Include header comment with description and usage

#### Example Script Template
```bash
#!/usr/bin/env bash
# Description: Install and manage Nomad on macOS
# Usage: ./nomad-install.sh [start|stop|restart|destroy]

set -euo pipefail

# Constants in UPPERCASE
readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly NOMAD_VERSION="latest"

# Functions in lowercase with underscores
main() {
    local command="${1:-}"

    case "${command}" in
        start) start_nomad ;;
        stop) stop_nomad ;;
        restart) restart_nomad ;;
        destroy) destroy_nomad ;;
        *) usage ;;
    esac
}

usage() {
    cat << EOF
Usage: $0 [COMMAND]

Commands:
    start    Start Nomad cluster
    stop     Stop Nomad cluster
    restart  Restart Nomad cluster
    destroy  Destroy Nomad cluster and cleanup

EOF
}

start_nomad() {
    echo "Starting Nomad..."
    # Implementation here
}

# Main execution
main "$@"
```

#### Naming Conventions
- Variables: `lowercase_with_underscores`
- Functions: `lowercase_with_underscores`
- Constants: `UPPERCASE_WITH_UNDERSCORES`
- Environment variables: `UPPERCASE_WITH_UNDERSCORES`

#### Error Handling
- Always use `set -euo pipefail`
- Check command success with `||` or `if`
- Use descriptive error messages
- Cleanup on failure with `trap`

```bash
cleanup() {
    echo "Cleaning up..."
    # Cleanup logic
}

trap cleanup EXIT

# Or for specific signals
trap 'error_exit "Script interrupted"' INT TERM
```

#### Command Substitution
```bash
# Preferred: Use $() over backticks
current_dir="$(pwd)"
version="$(nomad version | head -n1)"

# Avoid: backticks
# current_dir=`pwd`
```

#### Conditionals and Loops
```bash
# Use [[ ]] for string/file tests
if [[ -f "config.hcl" ]]; then
    echo "Config file exists"
fi

# Use (( )) for arithmetic
if (( port > 1024 )); then
    echo "Using high port"
fi

# Use arrays for multiple values
local services=("consul" "vault" "nomad")
for service in "${services[@]}"; do
    echo "Processing ${service}"
done
```

### Configuration Files

#### HCL (HashiCorp Configuration Language)
- Use for Nomad/Vault/Consul configs
- 2-space indentation
- Group related settings
- Use comments for complex configurations

```hcl
# Nomad server configuration
server {
  enabled = true
  bootstrap_expect = 1

  server_join {
    retry_join = ["127.0.0.1:4648"]
  }
}

# TLS configuration
tls {
  http = true
  rpc  = true
  ca_file   = "/etc/nomad.d/tls/ca.pem"
  cert_file = "/etc/nomad.d/tls/nomad.pem"
  key_file  = "/etc/nomad.d/tls/nomad-key.pem"
}
```

#### YAML/JSON
- Use 2-space indentation
- Prefer YAML for human-editable configs
- Use JSON for machine-generated configs
- Validate with `yamllint` or `jq`

### Go Code Style

#### File Structure
- Use `main.go` for the main package
- Import standard library first, then third-party packages
- Use `go mod` for dependency management

#### Naming Conventions
- Variables and functions: `camelCase`
- Types and structs: `PascalCase`
- Constants: `PascalCase` or `ALL_CAPS`
- Exported functions/types: `PascalCase`

#### Error Handling
- Return errors from functions
- Use `if err != nil` pattern
- Handle errors at appropriate levels
- Use descriptive error messages

#### Formatting
- Use `go fmt` for consistent formatting
- Follow standard Go conventions
- Use `gofmt` or `goimports` for imports

### Documentation

#### README Files
- Include setup instructions
- Document all command-line options
- Provide examples for common use cases
- Include troubleshooting section

#### Inline Comments
- Explain complex logic
- Document function parameters and return values
- Comment non-obvious code sections

```bash
# Calculate retry delay with exponential backoff
# Args: attempt_number (int), base_delay (float)
calculate_delay() {
    local attempt="$1"
    local base="$2"
    echo "$base * 2 ^ ($attempt - 1)" | bc -l
}
```

### Security Best Practices

#### File Permissions
- Scripts: `755` (rwxr-xr-x)
- Config files with secrets: `600` (rw-------)
- Data directories: `700` (rwx------)

#### Secret Handling
- Never commit secrets to repository
- Use environment variables for sensitive data
- Validate TLS certificates
- Use secure defaults

```bash
# Environment variable for license
readonly NOMAD_LICENSE="${NOMAD_LICENSE:-}"

if [[ -z "${NOMAD_LICENSE}" ]]; then
    echo "Error: NOMAD_LICENSE environment variable required"
    exit 1
fi
```

#### TLS Configuration
- Always enable TLS in production
- Use strong ciphers
- Rotate certificates regularly
- Validate certificate chains

### Testing Guidelines

#### Unit Tests
- Test functions in isolation
- Mock external dependencies
- Test error conditions
- Use descriptive test names

```bash
@test "install_nomad downloads correct version" {
    # Test implementation
    run install_nomad "1.5.0"
    [ "$status" -eq 0 ]
    [ -f "/usr/local/bin/nomad" ]
}
```

#### Integration Tests
- Test complete workflows
- Use temporary directories
- Cleanup after tests
- Test with real dependencies when safe

### Git Workflow

#### Commit Messages
- Use imperative mood: "Add", "Fix", "Update"
- Include component name: "nomad: Add TLS validation"
- Keep first line under 50 characters
- Add body for complex changes

```
feat: Add Vault integration with TLS

- Implement automatic Vault certificate management
- Add health checks for Vault connectivity
- Update documentation with Vault setup steps

Closes #123
```

#### Branch Naming
- Feature branches: `feature/description`
- Bug fixes: `fix/issue-description`
- Hotfixes: `hotfix/critical-bug`

#### .gitignore
- Include a `.gitignore` file in the repository root
- Ignore local installation directories (e.g., `nomad-local/`)
- Ignore log files (*.log), temporary files (*.tmp, *.swp), and OS-specific files (.DS_Store, Thumbs.db)
- Ensure no sensitive or temporary data is committed

### CI/CD Integration

#### GitHub Actions
- Use matrix builds for multiple macOS versions
- Cache dependencies
- Run tests in parallel
- Upload artifacts on failure

#### Pre-commit Hooks
- Install shellcheck and shfmt
- Run on commit
- Block commits with lint errors

### Dependency Management

#### External Tools
- Pin versions in scripts
- Verify checksums for downloads
- Use official installation methods
- Document version requirements

```bash
# Pin Nomad version
readonly NOMAD_VERSION="1.6.0"
readonly NOMAD_CHECKSUM="abc123..."

# Verify download
if ! echo "${NOMAD_CHECKSUM}  nomad.zip" | shasum -c -; then
    echo "Checksum verification failed"
    exit 1
fi
```

### Performance Considerations

#### Script Optimization
- Avoid unnecessary subprocesses
- Use built-in shell features
- Cache expensive operations
- Profile with `time` and `strace`

#### Resource Management
- Cleanup temporary files
- Handle interrupts gracefully
- Limit concurrent operations
- Monitor memory usage

### Cross-Platform Compatibility

#### macOS Specific
- Use `brew` for package management
- Handle macOS path differences
- Test on multiple macOS versions
- Use `launchctl` for services

#### Linux Compatibility
- Test on common distributions
- Handle different init systems
- Use `systemd` or `sysvinit` appropriately
- Check for required tools

### Monitoring and Logging

#### Log Levels
- ERROR: Fatal errors requiring attention
- WARN: Potential issues
- INFO: Normal operations
- DEBUG: Detailed debugging information

#### Structured Logging
```bash
log_error() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] ERROR: $*" >&2
}

log_info() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] INFO: $*"
}
```

### Maintenance

#### Code Reviews
- Require review for all changes
- Check for security issues
- Verify tests pass
- Ensure documentation updates

#### Version Updates
- Monitor for new releases
- Update dependencies regularly
- Test upgrades in staging
- Document breaking changes

### Troubleshooting

#### Common Issues
- Permission denied: Check file permissions
- Command not found: Verify PATH and installation
- Connection refused: Check service status and ports
- TLS errors: Validate certificates and trust stores

#### Debug Mode
```bash
if [[ "${DEBUG:-false}" == "true" ]]; then
    set -x
    export PS4='+(${BASH_SOURCE}:${LINENO}): ${FUNCNAME[0]:+${FUNCNAME[0]}(): }'
fi
```

This guide ensures consistent, maintainable, and secure code across all contributions to this project.