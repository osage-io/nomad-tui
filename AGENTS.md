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

## Nomad TUI

The `nomad-tui` is a comprehensive terminal user interface (TUI) for monitoring and managing Nomad clusters, built with Go and Bubbletea.

### Features

#### Core Views
- **Jobs View**: Browse all jobs with filtering, multi-selection, and job details
- **Nodes View**: View all client nodes with status and resource utilization
- **Services View**: Monitor Nomad native service registrations
- **Cluster View**: High-level cluster metrics and resource utilization
- **Job Details**: Comprehensive job information with allocation management
- **Node Details**: Detailed node information with resource allocation
- **Allocation Details**: Deep dive into individual allocation state and resources
- **Evaluation Details**: View evaluation status and placement decisions
- **Log Viewer**: Real-time log streaming for jobs and allocations (stdout/stderr)
- **Events Viewer**: Monitor task events and state changes

#### Job Management
- Start, stop, and delete jobs (single or multi-select)
- View job status, allocations, and evaluations
- Navigate between jobs with n/p keys
- Placement failure warnings and diagnostics
- Job filtering by name

#### Allocation Management
- Stop and restart individual allocations
- View allocation logs in real-time
- View allocation events and state transitions
- Navigate allocations within job details
- CPU and memory usage tracking per allocation

#### Node Management
- View node status, capacity, and availability
- Monitor resource utilization (CPU, memory)
- View node-level details (drivers, host volumes, datacenter, node pool)
- Navigate between nodes with n/p keys

#### Service Discovery
- View all Nomad native service registrations
- Service name, tags, address, port, and associated job
- Filter services by name

#### Advanced Features
- **Multi-selection**: Select multiple jobs with Shift+↑/↓ for bulk operations
- **Filtering**: Press `/` or `f` to filter jobs, nodes, or services by name
- **Blocking Queries**: Automatic real-time updates when cluster state changes (using Nomad blocking queries)
- **Responsive Tables**: Column widths adjust proportionally to terminal size
- **Smart Scrolling**: List and page-level scrolling with visual indicators
- **Follow Mode**: Auto-scroll to bottom of logs (toggle with `p`)
- **Color Themes**: 8 HashiCorp product themes (nomad, vault, consul, boundary, packer, terraform, waypoint, vagrant)
- **TLS Support**: Full TLS certificate verification with skip option for dev
- **ACL Authentication**: Token-based authentication support
- **Environment Variables**: Configure via NOMAD_ADDR, NOMAD_TOKEN, NOMAD_SKIP_VERIFY

#### Navigation
- Arrow key navigation (←/→ for view switching, ↑/↓ for selection)
- Vim-style navigation (j for jobs, n for nodes, c for cluster, v for services)
- Enter key to drill down into details
- Esc key to navigate back
- Home/End for jumping to first/last items
- PgUp/PgDown for fast scrolling

### Building and Running
```bash
# Build the binary
go build -o nomad-tui .

# Run with default settings (localhost:4646)
./nomad-tui

# Run with custom Nomad server
./nomad-tui -addr https://nomad.example.com:4646

# Run with ACL token
./nomad-tui -addr https://nomad.example.com:4646 -token YOUR_TOKEN

# Run with TLS verification skip (dev only)
./nomad-tui -skip-verify

# Run with a specific theme
./nomad-tui -theme vault
```

### Command-Line Flags
- `-addr`: Nomad server address (default: http://localhost:4646 or $NOMAD_ADDR)
- `-token`: Nomad ACL token (default: $NOMAD_TOKEN)
- `-skip-verify`: Skip TLS certificate verification (default: false)
- `-theme`: Color theme - nomad, vault, consul, boundary, packer, terraform, waypoint, vagrant (default: nomad)

### Keyboard Controls

#### Global
- `q` or `Ctrl+C`: Quit the application
- `r`: Refresh data manually
- `h`: Toggle help screen
- `←`/`→`: Switch between main views (Jobs → Nodes → Services → Cluster)
- `/` or `f`: Activate filter mode

#### View Switching
- `j`: Switch to jobs view
- `n`: Switch to nodes view (or next job/node in detail views)
- `v`: Switch to services view
- `c`: Switch to cluster view
- `p`: Previous job/node in detail views, or toggle pause/follow in logs view
- `b`: Back to previous view (from logs/events)
- `Esc`: Back to parent view

#### Jobs View
- `↑`/`↓`: Navigate job list
- `Shift+↑`/`Shift+↓`: Multi-select jobs
- `Enter` or `i`: View job details
- `s`: Stop selected job (single only)
- `d`: Delete selected job(s) (supports multi-select)

#### Job Details View
- `↑`/`↓`: Scroll content
- `a`: Enter allocation selection mode
- `e`: View job events
- `l`: View job logs
- `n`/`p`: Next/Previous job
- `Esc`: Back to jobs list

#### Allocation Selection Mode (in Job Details)
- `↑`/`↓`: Navigate allocations
- `Enter`: View allocation details
- `s`: Stop selected allocation
- `x`: Restart selected allocation
- `l`: View allocation logs
- `Esc`: Exit allocation mode

#### Nodes View
- `↑`/`↓`: Navigate node list
- `Enter`: View node details

#### Node Details View
- `↑`/`↓`: Scroll content
- `n`/`p`: Next/Previous node
- `Esc`: Back to nodes list

#### Services View
- `↑`/`↓`: Navigate service list

#### Cluster View
- `↑`/`↓`: Scroll content

#### Logs View
- `↑`/`↓`: Scroll log content
- `p`: Toggle pause/follow mode
- `r`: Refresh logs
- `b` or `Esc`: Back to previous view
- `Home`/`End`: Jump to top/bottom
- `PgUp`/`PgDn`: Fast scroll

#### Help View
- `↑`/`↓`: Scroll help content
- `h` or `Esc`: Close help

#### Filter Mode
- Type to filter items by name
- `Backspace`: Delete characters
- `Enter`: Accept filter and exit filter mode
- `Esc`: Cancel filter and clear

### Terminal Compatibility
- Uses alternate screen mode for full terminal utilization
- Headers positioned properly to ensure visibility in all terminals
- Responsive to window resizing, adapting content to fit height and width
- Supports Unicode and ANSI color codes (256-color terminals recommended)
- Minimum recommended terminal size: 80x24

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