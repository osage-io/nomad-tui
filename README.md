# nomad-tui

A beautiful, interactive terminal user interface (TUI) for monitoring and managing HashiCorp Nomad clusters.

![nomad-tui demo](screenshots/demo.gif)
<!-- TODO: Add demo GIF screenshot -->

## Features

- 📊 **Real-time Monitoring** - View jobs, nodes, services, and cluster metrics with automatic updates using Nomad blocking queries
- 🎨 **Multiple Color Themes** - Support for Nomad, Vault, Consul, Boundary, Packer, Terraform, Waypoint, and Vagrant themes
- 📱 **Responsive Design** - Automatically adapts to terminal window size
- ⌨️ **Keyboard Navigation** - Full keyboard control with vim-like navigation
- 🔍 **Detailed Views** - Drill down into jobs, nodes, allocations, and evaluations for detailed information
- 🚀 **Job Management** - Stop, delete, and monitor jobs directly from the TUI (supports multi-selection)
- 📜 **Log Viewing** - View allocation logs in real-time with follow mode
- 📋 **Event Tracking** - Monitor job and allocation events with state changes
- 🔐 **Secure Connections** - Support for TLS and ACL tokens
- 🎯 **Allocation Management** - Stop, restart, and view logs for individual allocations
- 🔎 **Filtering** - Filter jobs, nodes, and services by name with live search
- ✅ **Multi-Selection** - Select and delete multiple jobs at once with Shift+↑/↓
- 🔄 **Blocking Queries** - Automatic real-time updates when cluster state changes

## Screenshots

### Jobs View
![Jobs View](screenshots/jobs-view.png)
<!-- TODO: Add jobs view screenshot showing the jobs table with status indicators -->

### Nodes View
![Nodes View](screenshots/nodes-view.png)
<!-- TODO: Add nodes view screenshot showing node list with health status -->

### Cluster Overview
![Cluster Overview](screenshots/cluster-view.png)
<!-- TODO: Add cluster overview screenshot showing CPU/Memory metrics -->

### Job Details
![Job Details](screenshots/job-details.png)
<!-- TODO: Add job details screenshot showing allocations and evaluations -->

### Help Screen
![Help Screen](screenshots/help-screen.png)
<!-- TODO: Add help screen screenshot showing all keyboard shortcuts -->

## Installation

### Quick Install (Recommended)

Install the latest release with a single command:

```bash
curl -fsSL https://raw.githubusercontent.com/osage-io/nomad-tui/main/install.sh | bash
```

This will automatically:
- Detect your operating system and architecture
- Download the appropriate binary
- Install to `~/.local/bin/nomad-tui`
- Make the binary executable

**Custom install location:**

```bash
curl -fsSL https://raw.githubusercontent.com/osage-io/nomad-tui/main/install.sh | INSTALL_DIR=/usr/local/bin bash
```

### Manual Download

Download the latest release for your platform from the [releases page](https://github.com/osage-io/nomad-tui/releases/latest):

- **Linux AMD64**: `nomad-tui-VERSION-linux-amd64.tar.gz`
- **Linux ARM64**: `nomad-tui-VERSION-linux-arm64.tar.gz`
- **macOS Intel**: `nomad-tui-VERSION-darwin-amd64.tar.gz`
- **macOS Apple Silicon**: `nomad-tui-VERSION-darwin-arm64.tar.gz`

```bash
# Download and extract (replace VERSION and PLATFORM)
curl -LO https://github.com/osage-io/nomad-tui/releases/download/VERSION/nomad-tui-VERSION-PLATFORM.tar.gz
tar -xzf nomad-tui-VERSION-PLATFORM.tar.gz

# Move to your PATH
mv nomad-tui-PLATFORM /usr/local/bin/nomad-tui
chmod +x /usr/local/bin/nomad-tui
```

### From Source

**Prerequisites:**
- Go 1.21 or later
- Access to a Nomad cluster

```bash
# Clone the repository
git clone https://github.com/osage-io/nomad-tui.git
cd nomad-tui

# Build the binary
go build -o nomad-tui .

# Optionally, move to your PATH
sudo mv nomad-tui /usr/local/bin/
```

### Using Go Install

```bash
go install github.com/osage-io/nomad-tui@latest
```

## Usage

### Basic Usage

```bash
# Connect to default Nomad server (localhost:4646)
./nomad-tui

# Connect to a specific Nomad server
./nomad-tui -addr https://nomad.example.com:4646

# Use ACL token authentication
./nomad-tui -addr https://nomad.example.com:4646 -token YOUR_TOKEN

# Skip TLS certificate verification (not recommended for production)
./nomad-tui -addr https://nomad.example.com:4646 -skip-verify

# Use a different color theme
./nomad-tui -theme vault
```

### Command-Line Options

| Flag | Description | Default |
|------|-------------|---------|
| `-addr` | Nomad server address | `http://localhost:4646` |
| `-token` | Nomad ACL token | (none) |
| `-skip-verify` | Skip TLS certificate verification | `false` |
| `-theme` | Color theme (nomad, vault, consul, boundary, packer, terraform, waypoint, vagrant) | `nomad` |

### Environment Variables

You can also configure nomad-tui using environment variables:

```bash
export NOMAD_ADDR=https://nomad.example.com:4646
export NOMAD_TOKEN=your-acl-token
export NOMAD_SKIP_VERIFY=true  # Not recommended for production

./nomad-tui
```

## Keyboard Shortcuts

### Global Navigation

| Key | Action |
|-----|--------|
| `q` or `Ctrl+C` | Quit the application |
| `h` | Toggle help screen |
| `r` | Refresh data |
| `←` / `→` | Switch between main views (Jobs → Nodes → Services → Cluster) |
| `/` or `f` | Activate filter mode |
| `Home` / `End` | Jump to first/last item in list |
| `PgUp` / `PgDn` | Fast scroll through content |

### View Switching

| Key | Action |
|-----|--------|
| `j` | Switch to jobs view |
| `n` | Switch to nodes view (or next job/node in detail views) |
| `v` | Switch to services view |
| `c` | Switch to cluster view |
| `p` | Previous job/node in detail views, or toggle pause/follow in logs view |
| `b` | Back to previous view (from logs/events) |
| `Esc` | Back to parent view |

### View-Specific Navigation

#### Jobs View

| Key | Action |
|-----|--------|
| `↑` / `↓` | Navigate job list |
| `Shift+↑` / `Shift+↓` | Multi-select jobs |
| `Enter` or `i` | View job details |
| `s` | Stop selected job (single selection only) |
| `d` | Delete selected job(s) (supports multi-selection) |
| `/` or `f` | Filter jobs by name |

#### Nodes View

| Key | Action |
|-----|--------|
| `↑` / `↓` | Navigate node list |
| `Enter` | View node details |
| `/` or `f` | Filter nodes by name |

#### Services View

| Key | Action |
|-----|--------|
| `↑` / `↓` | Navigate service list |
| `/` or `f` | Filter services by name |

#### Cluster View

| Key | Action |
|-----|--------|
| `↑` / `↓` | Scroll page content |

#### Job Details View

| Key | Action |
|-----|--------|
| `↑` / `↓` | Scroll page content |
| `a` | Enter allocation selection mode |
| `n` / `p` | Next/Previous job |
| `l` | View job logs |
| `e` | View job events |
| `Enter` | Enter evaluation selection mode (when evaluations are visible) |
| `Esc` | Back to jobs list |

#### Allocation Selection Mode (in Job Details)

| Key | Action |
|-----|--------|
| `↑` / `↓` | Navigate allocations |
| `Enter` | View allocation details |
| `s` | Stop selected allocation |
| `x` | Restart selected allocation |
| `l` | View allocation logs |
| `Esc` | Exit allocation mode |

#### Allocation Details View

| Key | Action |
|-----|--------|
| `↑` / `↓` | Scroll page content |
| `e` | View allocation events |
| `l` | View allocation logs |
| `Esc` | Back to job details |

#### Evaluation Details View

| Key | Action |
|-----|--------|
| `↑` / `↓` | Scroll page content |
| `Esc` | Back to job details |

#### Logs View

| Key | Action |
|-----|--------|
| `↑` / `↓` | Scroll log content |
| `p` | Toggle pause/follow mode |
| `r` | Refresh logs |
| `b` or `Esc` | Back to previous view |
| `Home` / `End` | Jump to top/bottom |
| `PgUp` / `PgDn` | Fast scroll |

#### Events View

| Key | Action |
|-----|--------|
| `↑` / `↓` | Scroll events content |
| `b` or `Esc` | Back to previous view |

#### Filter Mode

| Key | Action |
|-----|--------|
| Type | Add characters to filter |
| `Backspace` | Delete characters |
| `Enter` | Accept filter and exit filter mode |
| `Esc` | Cancel filter and clear |

#### Node Details View

| Key | Action |
|-----|--------|
| `↑` / `↓` | Scroll page content |
| `n` / `p` | Next/Previous node |
| `Esc` | Back to nodes list |

#### Help View

| Key | Action |
|-----|--------|
| `↑` / `↓` | Scroll help content |
| `h` / `Esc` | Close help |

## Views

### Jobs View

The Jobs view displays all jobs in the Nomad cluster with:
- Job name
- Status (running, pending, dead) with color-coded indicators
- Job type (service, batch, system)
- Node pool
- Uptime
- Placement failure warnings (⚠) when evaluations are blocked or failed
- Allocation status breakdown (e.g., [running:3, pending:1])
- Multi-selection support with visual indicators
- Filter by job name
- Automatic scrolling for long job lists with scroll position indicator
- Jobs sorted with running jobs first

### Nodes View

The Nodes view shows all client nodes with:
- Node ID (shortened to 8 characters)
- Node name
- Datacenter
- Operating system and version
- Status (ready, down) with color-coded indicators
- Nomad version
- IP address
- Node pool
- Filter by node name
- Automatic scrolling for large node lists with scroll position indicator

### Services View

The Services view lists all registered Nomad native services with:
- Service name
- Tags (comma-separated)
- Address and port
- Associated job ID
- Node ID (shortened)
- Datacenter
- Namespace
- Filter by service name
- Automatic scrolling for many services with scroll position indicator

### Cluster Overview

The Cluster view provides high-level metrics:
- Total nodes count with status breakdown (ready, initializing, down)
- Total jobs count with status breakdown (running, pending, dead)
- CPU utilization with visual progress bar showing used/reserved/available
- Memory utilization with visual progress bar showing used/reserved/available
- Real-time resource calculations across all nodes
- Scrollable content for detailed information

### Job Details View

The Job Details view shows comprehensive job information:
- Job status, type, priority, and metadata
- Node pool assignment
- Datacenters
- Resource allocation summary (CPU and memory across all allocations)
- Allocation status breakdown with counts
- **Allocations Table** with:
  - Allocation ID (shortened)
  - Node name
  - Task group
  - Status with color indicators
  - CPU usage (MHz)
  - Memory usage (MB)
  - Selection mode for management operations
- **Evaluations Table** with:
  - Evaluation ID (shortened)
  - Status (complete, pending, blocked, failed, canceled)
  - Priority
  - Triggered by (job registration, deployment, etc.)
  - Selection mode to view evaluation details
- **Placement Failure Details** (if any) showing:
  - Task group with failures
  - Constraint violations
  - Resource exhaustion
  - Node filtering results
- Allocation selection mode for operations (stop, restart, view logs/details)
- Navigation between jobs with n/p keys
- Scrollable content

### Node Details View

The Node Details view displays:
- Node status, scheduling eligibility, and drain status
- Nomad version
- Datacenter, class, and node pool
- Operating system name and version
- **Resource Capacity**:
  - Total CPU cores and compute units
  - Total memory (GB)
  - Available CPU and memory after reservations
- **Drivers** detected and enabled (docker, exec, java, etc.)
- **Host Volumes** configured on the node
- **Allocations** running on the node:
  - Count of running allocations
  - List of allocation IDs
- IP address
- Navigation between nodes with n/p keys
- Scrollable content

### Allocation Details View

The Allocation Details view provides:
- Allocation ID and status
- Associated job ID and task group
- Node name where allocation is running
- **Task States** for each task in the allocation:
  - Task name
  - State (running, pending, dead, failed)
  - Start time
  - Events count
- **Resource Usage**:
  - CPU allocation and usage
  - Memory allocation and usage
- **Task Events** for the allocation (accessible via `e` key)
- **Logs** for the allocation (accessible via `l` key)
- Scrollable content
- Navigation back to job details

### Evaluation Details View

The Evaluation Details view shows:
- Evaluation ID and status
- Job ID that triggered the evaluation
- Priority
- Triggered by (job registration, scaling, deployment, etc.)
- Status description
- Placement metrics showing:
  - Nodes evaluated
  - Nodes filtered
  - Constraint violations
  - Resource exhaustion
  - Placement failures by task group
- Scrollable content
- Navigation back to job details

## Color Themes

nomad-tui supports multiple color themes to match HashiCorp product branding:

- **nomad** (default) - Purple theme matching Nomad branding
- **vault** - Yellow/gold theme matching Vault
- **consul** - Pink/magenta theme matching Consul
- **boundary** - Orange theme matching Boundary
- **packer** - Blue theme matching Packer
- **terraform** - Purple theme matching Terraform
- **waypoint** - Teal/cyan theme matching Waypoint
- **vagrant** - Blue theme matching Vagrant

Use the `-theme` flag to select your preferred theme:

```bash
./nomad-tui -theme vault
```

## Features in Detail

### Responsive Tables

All tables automatically resize based on terminal window dimensions:
- Column widths adjust proportionally
- Content truncates gracefully to fit available space
- Scroll indicators show when content extends beyond view

### List Scrolling

Jobs, nodes, and services views support smooth scrolling:
- Lists scroll automatically when selection moves beyond visible area
- Scroll position indicator shows current view range (e.g., "1-20 of 45")
- 2-line buffer before scrolling for better visibility

### Page Scrolling

Detail views (cluster, job details, node details) support page-level scrolling:
- Use `↑`/`↓` arrows to scroll through content
- Page footer always visible showing navigation hints
- Scroll hint displays when more content is available

### Job Management

Manage jobs directly from the TUI:
- **Stop Job**: Press `s` to gracefully stop a running job (single selection only)
- **Delete Job**: Press `d` to purge a job from the cluster
- **Multi-Selection**: Use `Shift+↑`/`Shift+↓` to select multiple jobs, then press `d` to delete them all at once
- Confirmation prompts prevent accidental actions
- Multi-delete operations are deduplicated by job ID for safety

### Allocation Management

In Job Details view, enter allocation mode to manage individual allocations:
- **Stop Allocation**: Gracefully stop a running allocation
- **Restart Allocation**: Restart a failed or running allocation
- **View Logs**: Stream allocation logs in real-time
- **View Details**: Press `Enter` to see detailed allocation information including resource usage, task states, and events
- **View Events**: View task events and state transitions for an allocation
- Visual indicator shows when in allocation mode

### Log Viewing

View logs for jobs and allocations:
- Displays last 32KB of stdout/stderr
- Auto-refreshes when pressing `r`
- **Follow Mode**: Toggle with `p` to auto-scroll to bottom as logs update
- Shows allocation ID and task name context
- Scroll through logs with `↑`/`↓` or `PgUp`/`PgDn`
- Jump to top/bottom with `Home`/`End`
- Error messages displayed clearly if logs unavailable

### Event Tracking

Monitor job and allocation events in real-time:
- Task state changes (Started, Terminated, Killed, etc.)
- Allocation events with allocation ID and task name
- Evaluation status and placement decisions
- Timestamps and event types
- Color-coded event types for easy scanning
- Sort by time (most recent first)
- View events for specific allocations in allocation details view

### Filtering

Filter jobs, nodes, and services by name:
- Press `/` or `f` to activate filter mode
- Type to filter items by name (case-sensitive substring match)
- Press `Enter` to accept filter and exit filter mode
- Press `Esc` to cancel filter and clear
- Selection resets when filter changes
- Works in jobs, nodes, and services views

### Real-Time Updates

nomad-tui uses Nomad blocking queries for efficient real-time updates:
- Automatically refreshes when cluster state changes
- No polling overhead - updates only when data changes
- 30-second blocking query timeout for responsive UI
- Fallback to 30-second heartbeat tick if blocking queries fail
- Manual refresh available with `r` key

### Multi-Selection

Select multiple jobs for bulk operations:
- Use `Shift+↑` to extend selection upward
- Use `Shift+↓` to extend selection downward
- Selected jobs are highlighted
- Press `d` to delete all selected jobs at once
- Single-job operations (like stop) require exactly one job selected
- Selection is preserved when switching views
- Selection is cleared when filter is applied

## Troubleshooting

### Connection Issues

**Problem**: Cannot connect to Nomad server

**Solutions**:
- Verify the Nomad server address with `-addr` flag
- Check that Nomad server is running and accessible
- Ensure firewall rules allow connection to Nomad port (default: 4646)
- Verify network connectivity

### Authentication Issues

**Problem**: Permission denied errors

**Solutions**:
- Provide a valid ACL token with `-token` flag
- Set `NOMAD_TOKEN` environment variable
- Verify token has necessary permissions for operations

### TLS Certificate Issues

**Problem**: TLS certificate verification failures

**Solutions**:
- Ensure TLS certificates are valid and trusted
- Use `-skip-verify` for development/testing (not recommended for production)
- Add Nomad CA certificate to system trust store
- Verify certificate hostname matches server address

### Display Issues

**Problem**: UI elements misaligned or garbled

**Solutions**:
- Ensure terminal supports Unicode and 256 colors
- Try resizing terminal window
- Update terminal emulator to latest version
- Check terminal `TERM` environment variable is set correctly

## Development

### Building from Source

```bash
# Clone repository
git clone https://github.com/osage-io/nomad-tui.git
cd nomad-tui

# Install dependencies
go mod download

# Build
go build -o nomad-tui .

# Run tests (when available)
go test ./...
```

### Code Style

This project follows standard Go conventions:

```bash
# Format code
go fmt .

# Lint code
go vet .

# Run all checks
make lint
```

See [AGENTS.md](AGENTS.md) for detailed development guidelines.

### Contributing

Contributions are welcome! Please:

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## Dependencies

nomad-tui is built with:

- [Bubbletea](https://github.com/charmbracelet/bubbletea) - Terminal UI framework
- [tcell](https://github.com/gdamore/tcell) - Terminal handling
- [Nomad API](https://github.com/hashicorp/nomad/api) - Nomad Go client

## License

[Add your license information here]

## Acknowledgments

- HashiCorp for Nomad and the excellent Go API client
- Charm for the wonderful Bubbletea TUI framework
- The Go community for amazing tools and libraries

## Support

- Report bugs via [GitHub Issues](https://github.com/osage-io/nomad-tui/issues)
- Ask questions in [Discussions](https://github.com/osage-io/nomad-tui/discussions)
- Contribute improvements via [Pull Requests](https://github.com/osage-io/nomad-tui/pulls)

---

Made with ❤️ for the Nomad community
