package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/hashicorp/nomad/api"
	"golang.org/x/term"
)

type jobStats struct {
	*api.JobListStub
	avgCPU             float64
	avgMem             float64
	allocStatuses      map[string]int
	allocs             []*api.AllocationListStub
	allocMap           map[string]*api.Allocation
	nodePool           string
	hasPlacementIssues bool
	placementFailures  map[string]*api.AllocationMetric // task group -> failure metrics
	evaluations        []*api.Evaluation
}

type nodeStats struct {
	*api.NodeListStub
	fullNode        *api.Node
	availCPU        int
	availMem        int
	ip              string
	datacenter      string
	version         string
	isServer        bool
	isLeader        bool
	allocationCount int
	numCPUs         int
	totalMem        int
	nodePool        string
	osName          string
	osVersion       string
	allocationIDs   []string
	drivers         []string
	hostVolumes     []string
}

type Theme struct {
	Running     string
	Pending     string
	Dead        string
	Header      string
	UtilLow     string
	UtilMedium  string
	UtilHigh    string
	HighlightBg string
}

type model struct {
	view                 string
	jobs                 []*jobStats
	nodes                []*nodeStats
	services             []*serviceInfo
	totalAvailCPU        int
	totalAvailMem        int
	totalCapacityCPU     int
	totalCapacityMem     int
	totalReservedCPU     int
	totalUsedCPU         float64
	totalReservedMem     int
	totalUsedMem         float64
	err                  error
	client               *api.Client
	height               int
	width                int
	selectedIndex        int
	selectedNodeIndex    int
	selectedJobIndex     int
	selectedAllocIndex   int
	selectedServiceIndex int
	showHelp             bool
	theme                Theme
	confirmAction        string
	confirmJob           *jobStats
	confirmAlloc         *api.AllocationListStub
	logContent           string
	logJobName           string
	logAllocID           string
	logTaskName          string
	eventsList           []jobEvent
	eventsJobName        string
	scrollOffset         int  // scroll offset for scrollable views (job-status, node-status, cluster)
	helpScrollOffset     int  // scroll offset for help view
	jobsScrollOffset     int  // scroll offset for jobs list view
	nodesScrollOffset    int  // scroll offset for nodes list view
	servicesScrollOffset int  // scroll offset for services list view
	allocSelectMode      bool // when true, ↑/↓ navigates allocations instead of scrolling in job-status view
}

type jobEvent struct {
	Time    time.Time
	AllocID string
	Task    string
	Type    string
	Message string
}

type serviceInfo struct {
	Name         string
	Tags         []string
	Namespace    string
	Datacenter   string
	JobID        string
	Address      string
	Port         int
	AllocID      string
	NodeID       string
	Registration *api.ServiceRegistration
}

type dataMsg struct {
	jobs             []*jobStats
	nodes            []*nodeStats
	services         []*serviceInfo
	totalAvailCPU    int
	totalAvailMem    int
	totalCapacityCPU int
	totalCapacityMem int
	totalReservedCPU int
	totalUsedCPU     float64
	totalReservedMem int
	totalUsedMem     float64
}

type logsMsg struct {
	content  string
	jobName  string
	allocID  string
	taskName string
	err      error
}

type eventsMsg struct {
	events  []jobEvent
	jobName string
	err     error
}

func fetchData(client *api.Client) tea.Msg {
	jobs, _, err := client.Jobs().List(nil)
	if err != nil {
		return errMsg(err)
	}
	nodes, _, err := client.Nodes().List(nil)
	if err != nil {
		return errMsg(err)
	}

	leaderIP, err := client.Status().Leader()
	if err != nil {
		leaderIP = ""
	}

	totalCapacityCPU := 0
	totalCapacityMem := 0
	nodeStatsList := make([]*nodeStats, 0, len(nodes))
	processNode := func(node *api.NodeListStub, client *api.Client, leaderIP string, totalCapacityCPU *int, totalCapacityMem *int, nodeStatsList *[]*nodeStats) {
		availCPU := 0
		availMem := 0
		ip := ""
		datacenter := ""
		version := ""
		isServer := false
		isLeader := false
		allocationCount := 0
		numCPUs := 0
		totalMem := 0
		nodePool := ""
		osName := ""
		osVersion := ""
		allocationIDs := []string{}
		drivers := []string{}
		hostVolumes := []string{}
		fullNode, _, err := client.Nodes().Info(node.ID, nil)
		if err != nil {
			return
		}
		ip = ""
		datacenter = fullNode.Datacenter
		version = ""
		nodePool = fullNode.NodePool
		osName = ""
		totalCPUStr := ""
		totalMemStr := ""
		numCPUsStr := ""
		if fullNode.Attributes != nil {
			ip = fullNode.Attributes["unique.network.ip-address"]
			version = fullNode.Attributes["nomad.version"]
			osName = fullNode.Attributes["os.name"]
			osVersion = fullNode.Attributes["os.version"]
			totalCPUStr = fullNode.Attributes["cpu.totalcompute"]
			totalMemStr = fullNode.Attributes["memory.totalbytes"]
			numCPUsStr = fullNode.Attributes["cpu.numcores"]
		}
		isServer = fullNode.NodeClass == "system"
		isLeader = isServer && ip == leaderIP
		if totalCPUStr != "" {
			if tc, err := strconv.Atoi(totalCPUStr); err == nil {
				*totalCapacityCPU += tc
			}
		}
		if totalMemStr != "" {
			if tm, err := strconv.ParseInt(totalMemStr, 10, 64); err == nil {
				*totalCapacityMem += int(tm / 1024 / 1024)
				totalMem = int(tm / 1024 / 1024)
			}
		}
		if fullNode.Resources != nil {
			if fullNode.Resources.CPU != nil {
				*totalCapacityCPU += int(*fullNode.Resources.CPU)
			}
			if fullNode.Resources.MemoryMB != nil {
				*totalCapacityMem += int(*fullNode.Resources.MemoryMB / 1024)
				totalMem = int(*fullNode.Resources.MemoryMB / 1024)
			}
		}
		if fullNode.Resources != nil && fullNode.ReservedResources != nil {
			if fullNode.Resources.CPU != nil {
				availCPU = int(*fullNode.Resources.CPU) - int(fullNode.ReservedResources.Cpu.CpuShares)
			}
			if fullNode.Resources.MemoryMB != nil {
				availMem = int(*fullNode.Resources.MemoryMB) - int(fullNode.ReservedResources.Memory.MemoryMB)
			}
		}
		if numCPUsStr != "" {
			numCPUs, _ = strconv.Atoi(numCPUsStr)
		}
		allocs, _, allocErr := client.Nodes().Allocations(node.ID, nil)
		if allocErr == nil {
			allocationCount = len(allocs)
			for _, alloc := range allocs {
				allocationIDs = append(allocationIDs, alloc.ID)
			}
		}
		drivers = make([]string, 0, len(fullNode.Drivers))
		for driver, info := range fullNode.Drivers {
			if info.Detected {
				drivers = append(drivers, driver)
			}
		}
		hostVolumes = make([]string, 0, len(fullNode.HostVolumes))
		for vol := range fullNode.HostVolumes {
			hostVolumes = append(hostVolumes, vol)
		}
		*nodeStatsList = append(*nodeStatsList, &nodeStats{
			NodeListStub:    node,
			fullNode:        fullNode,
			availCPU:        availCPU,
			availMem:        availMem,
			ip:              ip,
			datacenter:      datacenter,
			version:         version,
			isServer:        isServer,
			isLeader:        isLeader,
			allocationCount: allocationCount,
			numCPUs:         numCPUs,
			totalMem:        totalMem,
			nodePool:        nodePool,
			osName:          osName,
			osVersion:       osVersion,
			allocationIDs:   allocationIDs,
			drivers:         drivers,
			hostVolumes:     hostVolumes,
		})
	}
	for _, node := range nodes {
		processNode(node, client, leaderIP, &totalCapacityCPU, &totalCapacityMem, &nodeStatsList)
	}

	totalAvailCPU := 0
	totalAvailMem := 0
	for _, ns := range nodeStatsList {
		totalAvailCPU += ns.availCPU
		totalAvailMem += ns.availMem
	}

	totalReservedCPU := 0
	totalUsedCPU := 0.0
	totalReservedMem := 0
	totalUsedMem := 0.0

	jobStatsList := make([]*jobStats, 0, len(jobs))
	for _, job := range jobs {
		allocs, _, err := client.Jobs().Allocations(job.ID, true, nil)
		allocStatuses := make(map[string]int)
		allocMap := make(map[string]*api.Allocation)
		nodePool := ""
		fullJob, _, fullErr := client.Jobs().Info(job.ID, nil)
		if fullErr == nil && fullJob.NodePool != nil {
			nodePool = *fullJob.NodePool
		}
		if err != nil {
			jobStatsList = append(jobStatsList, &jobStats{
				JobListStub:        job,
				avgCPU:             0,
				avgMem:             0,
				allocStatuses:      allocStatuses,
				allocs:             allocs,
				allocMap:           allocMap,
				nodePool:           nodePool,
				hasPlacementIssues: false,
				placementFailures:  nil,
				evaluations:        nil,
			})
			continue
		}
		for _, alloc := range allocs {
			allocStatuses[alloc.ClientStatus]++
			fullAlloc, _, err := client.Allocations().Info(alloc.ID, nil)
			if err != nil {
				continue
			}
			allocMap[alloc.ID] = fullAlloc
			if fullAlloc.Resources != nil {
				if fullAlloc.Resources.CPU != nil {
					totalReservedCPU += int(*fullAlloc.Resources.CPU)
				}
				if fullAlloc.Resources.MemoryMB != nil {
					totalReservedMem += int(*fullAlloc.Resources.MemoryMB)
				}
			}
		}
		var totalCPU, totalMem float64
		count := 0
		for _, alloc := range allocs {
			if alloc.ClientStatus != "running" {
				continue
			}
			fullAlloc, ok := allocMap[alloc.ID]
			if !ok {
				continue
			}
			stats, err := client.Allocations().Stats(fullAlloc, nil)
			if err != nil {
				continue
			}
			var cpu_used, mem_used float64
			if len(stats.Tasks) > 0 {
				for _, task := range stats.Tasks {
					if task.ResourceUsage != nil && task.ResourceUsage.CpuStats != nil && fullAlloc.Resources.CPU != nil {
						cpu_used += task.ResourceUsage.CpuStats.Percent / 100 * float64(*fullAlloc.Resources.CPU)
					}
					if task.ResourceUsage != nil && task.ResourceUsage.MemoryStats != nil {
						mem_used += float64(task.ResourceUsage.MemoryStats.Usage) / (1024 * 1024)
					}
				}
			}
			totalCPU += cpu_used
			totalMem += mem_used
			count++
		}
		totalUsedCPU += totalCPU
		totalUsedMem += totalMem
		avgCPU := totalCPU
		avgMem := totalMem
		if count > 0 {
			avgCPU /= float64(count)
			avgMem /= float64(count)
		}

		// Fetch evaluations to check for placement failures
		hasPlacementIssues := false
		placementFailures := make(map[string]*api.AllocationMetric)
		evals, _, evalErr := client.Jobs().Evaluations(job.ID, nil)
		if evalErr == nil && len(evals) > 0 {
			// Check the most recent evaluation for placement failures
			// Evaluations are returned in order, most recent first
			for _, eval := range evals {
				if eval.FailedTGAllocs != nil && len(eval.FailedTGAllocs) > 0 {
					hasPlacementIssues = true
					placementFailures = eval.FailedTGAllocs
					break
				}
				// Also check if there's a blocked evaluation
				if eval.Status == "blocked" {
					hasPlacementIssues = true
					if eval.FailedTGAllocs != nil {
						placementFailures = eval.FailedTGAllocs
					}
					break
				}
			}
		}

		jobStatsList = append(jobStatsList, &jobStats{
			JobListStub:        job,
			avgCPU:             avgCPU,
			avgMem:             avgMem,
			allocStatuses:      allocStatuses,
			allocs:             allocs,
			allocMap:           allocMap,
			nodePool:           nodePool,
			hasPlacementIssues: hasPlacementIssues,
			placementFailures:  placementFailures,
			evaluations:        evals,
		})
	}

	// Sort jobs: running first, then by name
	sort.Slice(jobStatsList, func(i, j int) bool {
		if jobStatsList[i].Status == "running" && jobStatsList[j].Status != "running" {
			return true
		}
		if jobStatsList[i].Status != "running" && jobStatsList[j].Status == "running" {
			return false
		}
		return jobStatsList[i].Name < jobStatsList[j].Name
	})

	// Fetch Nomad native services
	servicesList := make([]*serviceInfo, 0)
	serviceStubs, _, err := client.Services().List(nil)
	if err == nil {
		for _, stub := range serviceStubs {
			if stub.Services != nil {
				for _, svcStub := range stub.Services {
					// Get full service registrations
					registrations, _, regErr := client.Services().Get(svcStub.ServiceName, nil)
					if regErr == nil && registrations != nil {
						for _, reg := range registrations {
							servicesList = append(servicesList, &serviceInfo{
								Name:         reg.ServiceName,
								Tags:         reg.Tags,
								Namespace:    reg.Namespace,
								Datacenter:   reg.Datacenter,
								JobID:        reg.JobID,
								Address:      reg.Address,
								Port:         reg.Port,
								AllocID:      reg.AllocID,
								NodeID:       reg.NodeID,
								Registration: reg,
							})
						}
					}
				}
			}
		}
	}

	// Sort services by name
	sort.Slice(servicesList, func(i, j int) bool {
		return servicesList[i].Name < servicesList[j].Name
	})

	return dataMsg{jobs: jobStatsList, nodes: nodeStatsList, services: servicesList, totalAvailCPU: totalAvailCPU, totalAvailMem: totalAvailMem, totalCapacityCPU: totalCapacityCPU, totalCapacityMem: totalCapacityMem, totalReservedCPU: totalReservedCPU, totalUsedCPU: totalUsedCPU, totalReservedMem: totalReservedMem, totalUsedMem: totalUsedMem}
}

func stopJob(client *api.Client, jobID string) tea.Msg {
	_, _, err := client.Jobs().Deregister(jobID, false, nil)
	if err != nil {
		return errMsg(err)
	}
	return refreshMsg{}
}

func deleteJob(client *api.Client, jobID string) tea.Msg {
	_, _, err := client.Jobs().Deregister(jobID, true, nil)
	if err != nil {
		return errMsg(err)
	}
	return refreshMsg{}
}

func stopAlloc(client *api.Client, allocID string) tea.Msg {
	// Fetch the full allocation first
	alloc, _, err := client.Allocations().Info(allocID, nil)
	if err != nil {
		return errMsg(fmt.Errorf("failed to get allocation info: %v", err))
	}
	// Stop the allocation
	_, err = client.Allocations().Stop(alloc, nil)
	if err != nil {
		return errMsg(err)
	}
	return refreshMsg{}
}

func restartAlloc(client *api.Client, allocID string, taskName string) tea.Msg {
	// Fetch the full allocation first
	alloc, _, err := client.Allocations().Info(allocID, nil)
	if err != nil {
		return errMsg(fmt.Errorf("failed to get allocation info: %v", err))
	}
	// Restart tasks in the allocation
	err = client.Allocations().Restart(alloc, taskName, nil)
	if err != nil {
		return errMsg(err)
	}
	return refreshMsg{}
}

func fetchLogs(client *api.Client, job *jobStats) tea.Cmd {
	return func() tea.Msg {
		// Find a running allocation for this job
		var allocID string
		var taskName string

		for _, alloc := range job.allocs {
			if alloc.ClientStatus == "running" {
				allocID = alloc.ID
				// Get the first task from TaskStates
				if fullAlloc, ok := job.allocMap[alloc.ID]; ok {
					for name := range fullAlloc.TaskStates {
						taskName = name
						break
					}
				}
				break
			}
		}

		if allocID == "" {
			return logsMsg{err: fmt.Errorf("no running allocations found for job %s", job.Name)}
		}

		if taskName == "" {
			return logsMsg{err: fmt.Errorf("no tasks found in allocation %s", allocID)}
		}

		// Fetch logs using AllocFS
		alloc, _, err := client.Allocations().Info(allocID, nil)
		if err != nil {
			return logsMsg{err: fmt.Errorf("failed to get allocation info: %v", err)}
		}

		// Helper function to read logs of a specific type
		readLogs := func(logType string) (string, error) {
			cancel := make(chan struct{})
			defer close(cancel)

			frames, errChan := client.AllocFS().Logs(alloc, false, taskName, logType, "end", -32768, cancel, nil)

			var logContent strings.Builder
			timeout := time.After(5 * time.Second)

		readLoop:
			for {
				select {
				case frame, ok := <-frames:
					if !ok {
						// Channel closed, we're done
						break readLoop
					}
					if frame != nil && len(frame.Data) > 0 {
						logContent.Write(frame.Data)
					}
				case err := <-errChan:
					if err != nil {
						return "", err
					}
				case <-timeout:
					break readLoop
				}
			}
			return logContent.String(), nil
		}

		// Fetch both stdout and stderr
		stdoutContent, stdoutErr := readLogs("stdout")
		stderrContent, stderrErr := readLogs("stderr")

		// Build combined content
		var content strings.Builder

		if stdoutErr != nil {
			content.WriteString(fmt.Sprintf("=== STDOUT ERROR: %v ===\n", stdoutErr))
		} else if stdoutContent != "" {
			content.WriteString("=== STDOUT ===\n")
			content.WriteString(stdoutContent)
			if !strings.HasSuffix(stdoutContent, "\n") {
				content.WriteString("\n")
			}
		}

		if stderrErr != nil {
			content.WriteString(fmt.Sprintf("=== STDERR ERROR: %v ===\n", stderrErr))
		} else if stderrContent != "" {
			if content.Len() > 0 {
				content.WriteString("\n")
			}
			content.WriteString("=== STDERR ===\n")
			content.WriteString(stderrContent)
		}

		finalContent := content.String()
		if finalContent == "" {
			finalContent = fmt.Sprintf("(No log output available for task '%s')", taskName)
		}

		return logsMsg{
			content:  finalContent,
			jobName:  job.Name,
			allocID:  allocID,
			taskName: taskName,
		}
	}
}

func fetchAllocLogs(client *api.Client, allocStub *api.AllocationListStub, jobName string) tea.Cmd {
	return func() tea.Msg {
		allocID := allocStub.ID
		taskGroup := allocStub.TaskGroup

		// Fetch full allocation info
		alloc, _, err := client.Allocations().Info(allocID, nil)
		if err != nil {
			return logsMsg{err: fmt.Errorf("failed to get allocation info: %v", err)}
		}

		// Find a task name from TaskStates
		var taskName string
		for name := range alloc.TaskStates {
			taskName = name
			break
		}

		if taskName == "" {
			// If no TaskStates, try to get task from the task group definition
			return logsMsg{err: fmt.Errorf("no tasks found in allocation %s (task group: %s)", allocID[:8], taskGroup)}
		}

		// Helper function to read logs of a specific type
		readLogs := func(logType string) (string, error) {
			cancel := make(chan struct{})
			defer close(cancel)

			frames, errChan := client.AllocFS().Logs(alloc, false, taskName, logType, "end", -32768, cancel, nil)

			var logContent strings.Builder
			timeout := time.After(5 * time.Second)

		readLoop:
			for {
				select {
				case frame, ok := <-frames:
					if !ok {
						// Channel closed, we're done
						break readLoop
					}
					if frame != nil && len(frame.Data) > 0 {
						logContent.Write(frame.Data)
					}
				case err := <-errChan:
					if err != nil {
						return "", err
					}
				case <-timeout:
					break readLoop
				}
			}
			return logContent.String(), nil
		}

		// Fetch both stdout and stderr
		stdoutContent, stdoutErr := readLogs("stdout")
		stderrContent, stderrErr := readLogs("stderr")

		// Build combined content
		var content strings.Builder

		if stdoutErr != nil {
			content.WriteString(fmt.Sprintf("=== STDOUT ERROR: %v ===\n", stdoutErr))
		} else if stdoutContent != "" {
			content.WriteString("=== STDOUT ===\n")
			content.WriteString(stdoutContent)
			if !strings.HasSuffix(stdoutContent, "\n") {
				content.WriteString("\n")
			}
		}

		if stderrErr != nil {
			content.WriteString(fmt.Sprintf("=== STDERR ERROR: %v ===\n", stderrErr))
		} else if stderrContent != "" {
			if content.Len() > 0 {
				content.WriteString("\n")
			}
			content.WriteString("=== STDERR ===\n")
			content.WriteString(stderrContent)
		}

		finalContent := content.String()
		if finalContent == "" {
			finalContent = fmt.Sprintf("(No log output available for task '%s')", taskName)
		}

		return logsMsg{
			content:  finalContent,
			jobName:  jobName,
			allocID:  allocID,
			taskName: taskName,
		}
	}
}

func fetchEvents(client *api.Client, job *jobStats) tea.Cmd {
	return func() tea.Msg {
		var events []jobEvent

		// Collect events from all allocations
		for _, alloc := range job.allocs {
			if fullAlloc, ok := job.allocMap[alloc.ID]; ok {
				allocIDShort := alloc.ID
				if len(allocIDShort) > 8 {
					allocIDShort = allocIDShort[:8]
				}

				// Iterate through all task states
				for taskName, taskState := range fullAlloc.TaskStates {
					if taskState != nil && taskState.Events != nil {
						for _, event := range taskState.Events {
							events = append(events, jobEvent{
								Time:    time.Unix(0, event.Time),
								AllocID: allocIDShort,
								Task:    taskName,
								Type:    event.Type,
								Message: event.DisplayMessage,
							})
						}
					}
				}
			}
		}

		// Sort events by time (most recent first)
		sort.Slice(events, func(i, j int) bool {
			return events[i].Time.After(events[j].Time)
		})

		if len(events) == 0 {
			return eventsMsg{err: fmt.Errorf("no events found for job %s", job.Name)}
		}

		return eventsMsg{
			events:  events,
			jobName: job.Name,
		}
	}
}

func ansiColor(status string, theme Theme) string {
	switch status {
	case "running", "ready":
		return theme.Running
	case "pending", "connecting":
		return theme.Pending
	case "dead", "failed", "down":
		return theme.Dead
	default:
		return ""
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// calculateColumnWidths distributes available width among columns proportionally
// weights defines the relative width of each column (e.g., []int{3, 2, 1, 2} means first col gets 3/8 of space)
// minWidths defines minimum width for each column
// availableWidth is the total width available for the table content (excluding borders/padding)
func calculateColumnWidths(availableWidth int, weights []int, minWidths []int) []int {
	numCols := len(weights)
	if numCols == 0 {
		return []int{}
	}

	// Calculate total weight
	totalWeight := 0
	for _, w := range weights {
		totalWeight += w
	}

	// Calculate proportional widths
	widths := make([]int, numCols)
	totalMinWidth := 0
	for i, min := range minWidths {
		totalMinWidth += min
		widths[i] = min
	}

	// If available width is less than minimum, just use minimums
	if availableWidth <= totalMinWidth {
		return widths
	}

	// Distribute remaining width proportionally
	remainingWidth := availableWidth - totalMinWidth
	for i, weight := range weights {
		extra := (remainingWidth * weight) / totalWeight
		widths[i] += extra
	}

	// Distribute any rounding remainder to the first column
	totalAllocated := 0
	for _, w := range widths {
		totalAllocated += w
	}
	if totalAllocated < availableWidth {
		widths[0] += availableWidth - totalAllocated
	}

	return widths
}

// getTableWidth returns the available width for table content
// Accounts for: 2 chars left margin + border chars between/around columns
func getTableWidth(termWidth int, numColumns int) int {
	// 2 for left margin "  ", 1 for each column border (numColumns + 1 total)
	overhead := 2 + numColumns + 1
	available := termWidth - overhead
	if available < numColumns*4 { // minimum 4 chars per column
		return numColumns * 4
	}
	return available
}

// getBoxWidth returns the width for header boxes based on terminal width
func getBoxWidth(termWidth int) int {
	// Leave some margin: 2 left + 2 right
	boxWidth := termWidth - 6
	if boxWidth < 40 {
		boxWidth = 40
	}
	if boxWidth > 120 {
		boxWidth = 120 // cap at reasonable max
	}
	return boxWidth
}

func formatAllocStatuses(statuses map[string]int) string {
	if len(statuses) == 0 {
		return ""
	}
	var parts []string
	for status, count := range statuses {
		parts = append(parts, fmt.Sprintf("%s:%d", status, count))
	}
	return " [" + strings.Join(parts, ", ") + "]"
}

// displayWidth calculates the visual width of a string in the terminal
// accounting for emojis (2 cells) and other wide characters
func displayWidth(s string) int {
	width := 0
	for _, r := range s {
		if r >= 0x1F300 && r <= 0x1F9FF { // Common emoji ranges
			width += 2
		} else if r >= 0x2600 && r <= 0x26FF { // Misc symbols
			width += 2
		} else if r >= 0x2700 && r <= 0x27BF { // Dingbats
			width += 2
		} else if r == 0xFE0F { // Variation selector (invisible)
			width += 0
		} else {
			width += 1
		}
	}
	return width
}

// renderHeader creates a consistent box header with proper alignment
// boxWidth is the inner width (between the ║ characters)
func renderHeader(title string, boxWidth int, headerColor string, bold string, reset string) string {
	topBorder := "  " + bold + headerColor + "╔" + strings.Repeat("═", boxWidth) + "╗" + reset + "\n"

	// Title with padding (account for 2 spaces before title)
	// Use displayWidth for proper emoji handling
	titleDisplayWidth := displayWidth(title) + 2 // "  " + title
	padding := boxWidth - titleDisplayWidth
	if padding < 0 {
		padding = 0
	}
	middleLine := "  " + bold + headerColor + "║" + reset + "  " + bold + title + reset + strings.Repeat(" ", padding) + bold + headerColor + "║" + reset + "\n"

	bottomBorder := "  " + bold + headerColor + "╚" + strings.Repeat("═", boxWidth) + "╝" + reset + "\n"

	return topBorder + middleLine + bottomBorder
}

type errMsg error

type refreshMsg struct{}

func (m model) Init() tea.Cmd {
	return tea.Batch(tea.ClearScreen, tea.Cmd(func() tea.Msg { return fetchData(m.client) }), tea.Tick(5*time.Second, func(t time.Time) tea.Msg {
		return tickMsg{}
	}))
}

type tickMsg time.Time

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.width = msg.Width
		return m, tea.ClearScreen
	case tea.KeyMsg:
		key := msg.String()
		if m.confirmAction != "" {
			switch key {
			case "y", "Y":
				var cmd tea.Cmd
				if m.confirmAlloc != nil {
					// Allocation actions
					allocID := m.confirmAlloc.ID
					if m.confirmAction == "stop-alloc" {
						cmd = tea.Cmd(func() tea.Msg { return stopAlloc(m.client, allocID) })
					} else if m.confirmAction == "restart-alloc" {
						cmd = tea.Cmd(func() tea.Msg { return restartAlloc(m.client, allocID, "") })
					}
					m.confirmAlloc = nil
				} else if m.confirmJob != nil {
					// Job actions
					jobID := m.confirmJob.ID
					if m.confirmAction == "stop" {
						cmd = tea.Cmd(func() tea.Msg { return stopJob(m.client, jobID) })
					} else {
						cmd = tea.Cmd(func() tea.Msg { return deleteJob(m.client, jobID) })
					}
					m.confirmJob = nil
				}
				m.confirmAction = ""
				return m, cmd
			case "n", "N", "esc":
				m.confirmAction = ""
				m.confirmJob = nil
				m.confirmAlloc = nil
				return m, nil
			}
			return m, nil
		}
		if m.showHelp {
			switch key {
			case "h", "esc":
				m.showHelp = false
				m.helpScrollOffset = 0
				return m, tea.ClearScreen
			case "q", "ctrl+c":
				return m, tea.Quit
			case "up":
				if m.helpScrollOffset > 0 {
					m.helpScrollOffset--
				}
				return m, nil
			case "down":
				m.helpScrollOffset++
				return m, nil
			case "j":
				m.showHelp = false
				m.helpScrollOffset = 0
				m.view = "jobs"
			case "n":
				m.showHelp = false
				m.helpScrollOffset = 0
				m.view = "nodes"
			case "r":
				return m, tea.Cmd(func() tea.Msg { return fetchData(m.client) })
			}
			return m, nil
		}
		switch key {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "r":
			if m.view == "job-logs" && m.selectedJobIndex >= 0 && m.selectedJobIndex < len(m.jobs) {
				selectedJob := m.jobs[m.selectedJobIndex]
				m.logContent = "Loading logs..."
				return m, fetchLogs(m.client, selectedJob)
			}
			if m.view == "job-events" && m.selectedJobIndex >= 0 && m.selectedJobIndex < len(m.jobs) {
				selectedJob := m.jobs[m.selectedJobIndex]
				m.eventsList = nil
				return m, fetchEvents(m.client, selectedJob)
			}
			return m, tea.Cmd(func() tea.Msg { return fetchData(m.client) })
		case "j":
			if m.view != "job-status" && m.view != "job-logs" && m.view != "job-events" && m.view != "node-status" {
				m.view = "jobs"
			}
		case "n":
			if m.view == "job-status" {
				// Next job
				if m.selectedJobIndex < len(m.jobs)-1 {
					m.selectedJobIndex++
					m.selectedAllocIndex = 0
					m.scrollOffset = 0
					m.allocSelectMode = false
				}
			} else if m.view == "node-status" {
				// Next node
				if m.selectedNodeIndex < len(m.nodes)-1 {
					m.selectedNodeIndex++
					m.scrollOffset = 0
				}
			} else if m.view != "job-logs" && m.view != "job-events" {
				m.view = "nodes"
			}
		case "p":
			if m.view == "job-status" {
				// Previous job
				if m.selectedJobIndex > 0 {
					m.selectedJobIndex--
					m.selectedAllocIndex = 0
					m.scrollOffset = 0
					m.allocSelectMode = false
				}
			} else if m.view == "node-status" {
				// Previous node
				if m.selectedNodeIndex > 0 {
					m.selectedNodeIndex--
					m.scrollOffset = 0
				}
			}
		case "b":
			if m.view == "job-logs" {
				m.view = "job-status"
			} else if m.view == "job-events" {
				m.view = "job-status"
			}
		case "esc":
			if m.view == "node-status" {
				m.view = "nodes"
			}
			if m.view == "job-status" {
				if m.allocSelectMode {
					// Exit allocation selection mode first
					m.allocSelectMode = false
				} else {
					m.view = "jobs"
				}
			}
			if m.view == "job-logs" {
				m.view = "job-status"
			}
			if m.view == "job-events" {
				m.view = "job-status"
			}
		case "v":
			m.view = "services"
		case "c":
			m.view = "cluster"
			m.scrollOffset = 0
		case "up":
			if m.view == "jobs" && m.selectedIndex > 0 {
				m.selectedIndex--
				// Scroll up if selection is above visible area
				if m.selectedIndex < m.jobsScrollOffset {
					m.jobsScrollOffset = m.selectedIndex
				}
			}
			if m.view == "nodes" && m.selectedNodeIndex > 0 {
				m.selectedNodeIndex--
				// Scroll up if selection is above visible area
				if m.selectedNodeIndex < m.nodesScrollOffset {
					m.nodesScrollOffset = m.selectedNodeIndex
				}
			}
			if m.view == "services" && m.selectedServiceIndex > 0 {
				m.selectedServiceIndex--
				// Scroll up if selection is above visible area
				if m.selectedServiceIndex < m.servicesScrollOffset {
					m.servicesScrollOffset = m.selectedServiceIndex
				}
			}
			if m.view == "job-status" {
				if m.allocSelectMode {
					// Navigate allocations
					if m.selectedAllocIndex > 0 {
						m.selectedAllocIndex--
					}
				} else if m.scrollOffset > 0 {
					m.scrollOffset--
				}
			}
			if m.view == "node-status" && m.scrollOffset > 0 {
				m.scrollOffset--
			}
			if m.view == "cluster" && m.scrollOffset > 0 {
				m.scrollOffset--
			}
		case "down":
			if m.view == "jobs" && m.selectedIndex < len(m.jobs)-1 {
				m.selectedIndex++
				// Calculate max visible jobs (same formula as in View)
				// Chrome = 15 lines (10 before table + 5 after)
				if m.height > 0 {
					chromeLines := 15
					maxVisibleJobs := m.height - chromeLines
					if maxVisibleJobs < 1 {
						maxVisibleJobs = 1
					}
					// Scroll down if selection is within 2 lines of bottom of visible area
					scrollBuffer := 2
					if m.selectedIndex >= m.jobsScrollOffset+maxVisibleJobs-scrollBuffer {
						m.jobsScrollOffset = m.selectedIndex - maxVisibleJobs + scrollBuffer + 1
					}
				}
			}
			if m.view == "nodes" && m.selectedNodeIndex < len(m.nodes)-1 {
				m.selectedNodeIndex++
				// Calculate max visible nodes (same chrome as jobs = 15 lines)
				if m.height > 0 {
					chromeLines := 15
					maxVisibleNodes := m.height - chromeLines
					if maxVisibleNodes < 1 {
						maxVisibleNodes = 1
					}
					// Scroll down if selection is within 2 lines of bottom of visible area
					scrollBuffer := 2
					if m.selectedNodeIndex >= m.nodesScrollOffset+maxVisibleNodes-scrollBuffer {
						m.nodesScrollOffset = m.selectedNodeIndex - maxVisibleNodes + scrollBuffer + 1
					}
				}
			}
			if m.view == "services" && m.selectedServiceIndex < len(m.services)-1 {
				m.selectedServiceIndex++
				// Calculate max visible services (same chrome as jobs = 15 lines)
				if m.height > 0 {
					chromeLines := 15
					maxVisibleServices := m.height - chromeLines
					if maxVisibleServices < 1 {
						maxVisibleServices = 1
					}
					// Scroll down if selection is within 2 lines of bottom of visible area
					scrollBuffer := 2
					if m.selectedServiceIndex >= m.servicesScrollOffset+maxVisibleServices-scrollBuffer {
						m.servicesScrollOffset = m.selectedServiceIndex - maxVisibleServices + scrollBuffer + 1
					}
				}
			}
			if m.view == "job-status" {
				if m.allocSelectMode {
					// Navigate allocations
					if m.selectedJobIndex >= 0 && m.selectedJobIndex < len(m.jobs) {
						selectedJob := m.jobs[m.selectedJobIndex]
						if m.selectedAllocIndex < len(selectedJob.allocs)-1 {
							m.selectedAllocIndex++
						}
					}
				} else {
					m.scrollOffset++
				}
			}
			if m.view == "node-status" {
				m.scrollOffset++
			}
			if m.view == "cluster" {
				m.scrollOffset++
			}
		case "s":
			if m.view == "jobs" && len(m.jobs) > 0 && m.confirmAction == "" {
				m.confirmAction = "stop"
				m.confirmJob = m.jobs[m.selectedIndex]
			} else if m.view == "job-status" && m.selectedJobIndex >= 0 && m.selectedJobIndex < len(m.jobs) && m.confirmAction == "" {
				selectedJob := m.jobs[m.selectedJobIndex]
				if m.selectedAllocIndex >= 0 && m.selectedAllocIndex < len(selectedJob.allocs) {
					m.confirmAction = "stop-alloc"
					m.confirmAlloc = selectedJob.allocs[m.selectedAllocIndex]
				}
			}
		case "x":
			if m.view == "job-status" && m.selectedJobIndex >= 0 && m.selectedJobIndex < len(m.jobs) && m.confirmAction == "" {
				selectedJob := m.jobs[m.selectedJobIndex]
				if m.selectedAllocIndex >= 0 && m.selectedAllocIndex < len(selectedJob.allocs) {
					m.confirmAction = "restart-alloc"
					m.confirmAlloc = selectedJob.allocs[m.selectedAllocIndex]
				}
			}
		case "d":
			if m.view == "jobs" && len(m.jobs) > 0 && m.confirmAction == "" {
				m.confirmAction = "delete"
				m.confirmJob = m.jobs[m.selectedIndex]
			}
		case "i":
			if m.view == "jobs" {
				m.view = "job-status"
				m.selectedJobIndex = m.selectedIndex
				m.selectedAllocIndex = 0
				m.scrollOffset = 0
			}
		case "enter":
			if m.view == "jobs" {
				m.view = "job-status"
				m.selectedJobIndex = m.selectedIndex
				m.selectedAllocIndex = 0
				m.scrollOffset = 0
			}
			if m.view == "nodes" {
				m.view = "node-status"
				m.scrollOffset = 0
			}
		case "l":
			if m.view == "job-status" && len(m.jobs) > 0 && m.selectedJobIndex >= 0 && m.selectedJobIndex < len(m.jobs) {
				selectedJob := m.jobs[m.selectedJobIndex]
				m.logContent = "Loading logs..."
				m.logJobName = selectedJob.Name
				m.view = "job-logs"
				// Use the selected allocation if one is selected and valid
				if m.selectedAllocIndex >= 0 && m.selectedAllocIndex < len(selectedJob.allocs) {
					selectedAlloc := selectedJob.allocs[m.selectedAllocIndex]
					return m, fetchAllocLogs(m.client, selectedAlloc, selectedJob.Name)
				}
				// Fall back to finding any running allocation
				return m, fetchLogs(m.client, selectedJob)
			}
		case "e":
			if m.view == "job-status" && len(m.jobs) > 0 && m.selectedJobIndex >= 0 && m.selectedJobIndex < len(m.jobs) {
				selectedJob := m.jobs[m.selectedJobIndex]
				m.eventsList = nil
				m.eventsJobName = selectedJob.Name
				m.view = "job-events"
				return m, fetchEvents(m.client, selectedJob)
			}
		case "a":
			// Toggle allocation selection mode in job-status view
			if m.view == "job-status" && m.selectedJobIndex >= 0 && m.selectedJobIndex < len(m.jobs) {
				selectedJob := m.jobs[m.selectedJobIndex]
				if len(selectedJob.allocs) > 0 {
					m.allocSelectMode = !m.allocSelectMode
					if m.allocSelectMode {
						// Entering alloc select mode, ensure valid selection
						if m.selectedAllocIndex < 0 || m.selectedAllocIndex >= len(selectedJob.allocs) {
							m.selectedAllocIndex = 0
						}
					}
				}
			}
		case "left":
			if m.view == "jobs" {
				m.view = "cluster"
			} else if m.view == "cluster" {
				m.view = "services"
			} else if m.view == "services" {
				m.view = "nodes"
			} else if m.view == "nodes" {
				m.view = "jobs"
			}
		case "right":
			if m.view == "jobs" {
				m.view = "nodes"
			} else if m.view == "nodes" {
				m.view = "services"
			} else if m.view == "services" {
				m.view = "cluster"
			} else if m.view == "cluster" {
				m.view = "jobs"
			}
		case "h":
			m.showHelp = true
			return m, tea.ClearScreen
		}
	case dataMsg:
		m.jobs = msg.jobs
		m.nodes = msg.nodes
		m.services = msg.services
		m.totalAvailCPU = msg.totalAvailCPU
		m.totalAvailMem = msg.totalAvailMem
		m.totalCapacityCPU = msg.totalCapacityCPU
		m.totalCapacityMem = msg.totalCapacityMem
		m.totalReservedCPU = msg.totalReservedCPU
		m.totalUsedCPU = msg.totalUsedCPU
		m.totalReservedMem = msg.totalReservedMem
		m.totalUsedMem = msg.totalUsedMem
		if m.selectedIndex >= len(m.jobs) {
			m.selectedIndex = 0
		}
		if len(m.nodes) > 0 {
			m.selectedNodeIndex = 0
		} else {
			m.selectedNodeIndex = -1
		}
		if m.selectedServiceIndex >= len(m.services) {
			m.selectedServiceIndex = 0
		}
		m.confirmAction = ""
		m.confirmJob = nil
		m.err = nil
		return m, tea.Tick(5*time.Second, func(t time.Time) tea.Msg { return tickMsg{} })
	case errMsg:
		m.err = error(msg)
		return m, nil
	case refreshMsg:
		return m, tea.Cmd(func() tea.Msg { return fetchData(m.client) })
	case logsMsg:
		if msg.err != nil {
			m.logContent = fmt.Sprintf("Error: %v", msg.err)
		} else {
			m.logContent = msg.content
			m.logAllocID = msg.allocID
			m.logTaskName = msg.taskName
		}
		return m, nil
	case eventsMsg:
		if msg.err != nil {
			m.eventsList = nil
		} else {
			m.eventsList = msg.events
		}
		return m, nil
	}
	return m, nil
}

func (m model) View() string {
	if m.width == 0 {
		m.width = 80
	}
	if m.height == 0 {
		m.height = 24
	}
	if m.height < 10 {
		m.height = 10
	}
	if m.height < 10 {
		return "Terminal height too small. Please resize to at least 10 lines.\n"
	}

	// Common styling used across views
	reset := "\033[0m"
	bold := "\033[1m"
	dimmed := "\033[2m"
	cyan := "\033[36m"

	// Dynamic box width based on terminal width
	boxWidth := getBoxWidth(m.width)

	content := ""
	if m.showHelp {
		// Colors for help page
		yellow := "\033[33m"
		green := "\033[32m"
		red := "\033[31m"
		keyColor := "\033[1;36m" // bold cyan for keys

		// Build help content (without header/footer - those are added separately)
		var helpLines []string

		// Column widths for tables
		colKey := 10
		colDesc := 28
		gap := "      " // gap between side-by-side columns

		// Left column visual width: 2 (indent) + 1 (│) + colKey + 1 (│) + colDesc + 1 (│) = 42
		leftVisualWidth := 2 + 1 + colKey + 1 + colDesc + 1

		// Helper function to pad line to fixed visual width
		padLeft := func(line string, visualLen int) string {
			padding := leftVisualWidth - visualLen
			if padding < 0 {
				padding = 0
			}
			return line + strings.Repeat(" ", padding)
		}

		// === ROW 1: NAVIGATION header / JOB ACTIONS header ===
		leftRow := "  " + bold + cyan + "NAVIGATION" + reset
		rightRow := bold + cyan + "JOB ACTIONS" + reset + "  " + dimmed + "(jobs view)" + reset
		helpLines = append(helpLines, padLeft(leftRow, 12)+gap+rightRow)

		// === ROW 2: NAVIGATION top border / JOB ACTIONS top border ===
		leftRow = "  " + dimmed + "╭" + strings.Repeat("─", colKey) + "┬" + strings.Repeat("─", colDesc) + "╮" + reset
		rightRow = dimmed + "╭" + strings.Repeat("─", colKey) + "┬" + strings.Repeat("─", colDesc) + "╮" + reset
		helpLines = append(helpLines, padLeft(leftRow, leftVisualWidth)+gap+rightRow)

		// === ROW 3: Column headers ===
		leftRow = "  " + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colKey-1, "Key") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colDesc-1, "Action") + reset + dimmed + "│" + reset
		rightRow = dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colKey-1, "Key") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colDesc-1, "Action") + reset + dimmed + "│" + reset
		helpLines = append(helpLines, padLeft(leftRow, leftVisualWidth)+gap+rightRow)

		// === ROW 4: Separators ===
		leftRow = "  " + dimmed + "├" + strings.Repeat("─", colKey) + "┼" + strings.Repeat("─", colDesc) + "┤" + reset
		rightRow = dimmed + "├" + strings.Repeat("─", colKey) + "┼" + strings.Repeat("─", colDesc) + "┤" + reset
		helpLines = append(helpLines, padLeft(leftRow, leftVisualWidth)+gap+rightRow)

		// === ROW 5: j / Enter ===
		leftRow = "  " + dimmed + "│" + reset + " " + keyColor + fmt.Sprintf("%-*s", colKey-1, "j") + reset + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colDesc-1, "Switch to jobs view") + dimmed + "│" + reset
		rightRow = dimmed + "│" + reset + " " + keyColor + fmt.Sprintf("%-*s", colKey-1, "Enter") + reset + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colDesc-1, "View job details") + dimmed + "│" + reset
		helpLines = append(helpLines, padLeft(leftRow, leftVisualWidth)+gap+rightRow)

		// === ROW 6: n / s ===
		leftRow = "  " + dimmed + "│" + reset + " " + keyColor + fmt.Sprintf("%-*s", colKey-1, "n") + reset + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colDesc-1, "Switch to nodes view") + dimmed + "│" + reset
		rightRow = dimmed + "│" + reset + " " + keyColor + fmt.Sprintf("%-*s", colKey-1, "s") + reset + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colDesc-1, "Stop selected job") + dimmed + "│" + reset
		helpLines = append(helpLines, padLeft(leftRow, leftVisualWidth)+gap+rightRow)

		// === ROW 7: v / d ===
		leftRow = "  " + dimmed + "│" + reset + " " + keyColor + fmt.Sprintf("%-*s", colKey-1, "v") + reset + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colDesc-1, "Switch to services view") + dimmed + "│" + reset
		rightRow = dimmed + "│" + reset + " " + keyColor + fmt.Sprintf("%-*s", colKey-1, "d") + reset + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colDesc-1, "Delete selected job") + dimmed + "│" + reset
		helpLines = append(helpLines, padLeft(leftRow, leftVisualWidth)+gap+rightRow)

		// === ROW 8: c / l ===
		leftRow = "  " + dimmed + "│" + reset + " " + keyColor + fmt.Sprintf("%-*s", colKey-1, "c") + reset + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colDesc-1, "Switch to cluster view") + dimmed + "│" + reset
		rightRow = dimmed + "│" + reset + " " + keyColor + fmt.Sprintf("%-*s", colKey-1, "l") + reset + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colDesc-1, "View job logs") + dimmed + "│" + reset
		helpLines = append(helpLines, padLeft(leftRow, leftVisualWidth)+gap+rightRow)

		// === ROW 9: ← → / e ===
		leftRow = "  " + dimmed + "│" + reset + " " + keyColor + fmt.Sprintf("%-*s", colKey-1, "← →") + reset + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colDesc-1, "Cycle through views") + dimmed + "│" + reset
		rightRow = dimmed + "│" + reset + " " + keyColor + fmt.Sprintf("%-*s", colKey-1, "e") + reset + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colDesc-1, "View job events") + dimmed + "│" + reset
		helpLines = append(helpLines, padLeft(leftRow, leftVisualWidth)+gap+rightRow)

		// === ROW 10: ↑ ↓ / bottom of JOB ACTIONS ===
		leftRow = "  " + dimmed + "│" + reset + " " + keyColor + fmt.Sprintf("%-*s", colKey-1, "↑ ↓") + reset + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colDesc-1, "Navigate list items") + dimmed + "│" + reset
		rightRow = dimmed + "╰" + strings.Repeat("─", colKey) + "┴" + strings.Repeat("─", colDesc) + "╯" + reset
		helpLines = append(helpLines, padLeft(leftRow, leftVisualWidth)+gap+rightRow)

		// === ROW 11: bottom of NAVIGATION / empty ===
		leftRow = "  " + dimmed + "╰" + strings.Repeat("─", colKey) + "┴" + strings.Repeat("─", colDesc) + "╯" + reset
		helpLines = append(helpLines, padLeft(leftRow, leftVisualWidth))

		// === ROW 12: empty ===
		helpLines = append(helpLines, "")

		// === ROW 13: GENERAL header / NODE ACTIONS header ===
		leftRow = "  " + bold + cyan + "GENERAL" + reset
		rightRow = bold + cyan + "NODE ACTIONS" + reset + "  " + dimmed + "(nodes view)" + reset
		helpLines = append(helpLines, padLeft(leftRow, 9)+gap+rightRow)

		// === ROW 14: GENERAL top border / NODE ACTIONS top border ===
		leftRow = "  " + dimmed + "╭" + strings.Repeat("─", colKey) + "┬" + strings.Repeat("─", colDesc) + "╮" + reset
		rightRow = dimmed + "╭" + strings.Repeat("─", colKey) + "┬" + strings.Repeat("─", colDesc) + "╮" + reset
		helpLines = append(helpLines, padLeft(leftRow, leftVisualWidth)+gap+rightRow)

		// === ROW 15: Column headers ===
		leftRow = "  " + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colKey-1, "Key") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colDesc-1, "Action") + reset + dimmed + "│" + reset
		rightRow = dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colKey-1, "Key") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colDesc-1, "Action") + reset + dimmed + "│" + reset
		helpLines = append(helpLines, padLeft(leftRow, leftVisualWidth)+gap+rightRow)

		// === ROW 16: Separators ===
		leftRow = "  " + dimmed + "├" + strings.Repeat("─", colKey) + "┼" + strings.Repeat("─", colDesc) + "┤" + reset
		rightRow = dimmed + "├" + strings.Repeat("─", colKey) + "┼" + strings.Repeat("─", colDesc) + "┤" + reset
		helpLines = append(helpLines, padLeft(leftRow, leftVisualWidth)+gap+rightRow)

		// === ROW 17: r / Enter ===
		leftRow = "  " + dimmed + "│" + reset + " " + keyColor + fmt.Sprintf("%-*s", colKey-1, "r") + reset + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colDesc-1, "Refresh data") + dimmed + "│" + reset
		rightRow = dimmed + "│" + reset + " " + keyColor + fmt.Sprintf("%-*s", colKey-1, "Enter") + reset + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colDesc-1, "View node details") + dimmed + "│" + reset
		helpLines = append(helpLines, padLeft(leftRow, leftVisualWidth)+gap+rightRow)

		// === ROW 18: b / bottom of NODE ACTIONS ===
		leftRow = "  " + dimmed + "│" + reset + " " + keyColor + fmt.Sprintf("%-*s", colKey-1, "b") + reset + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colDesc-1, "Go back to previous view") + dimmed + "│" + reset
		rightRow = dimmed + "╰" + strings.Repeat("─", colKey) + "┴" + strings.Repeat("─", colDesc) + "╯" + reset
		helpLines = append(helpLines, padLeft(leftRow, leftVisualWidth)+gap+rightRow)

		// === ROW 19: h / empty ===
		leftRow = "  " + dimmed + "│" + reset + " " + keyColor + fmt.Sprintf("%-*s", colKey-1, "h") + reset + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colDesc-1, "Toggle this help screen") + dimmed + "│" + reset
		helpLines = append(helpLines, padLeft(leftRow, leftVisualWidth))

		// === ROW 20: q / empty ===
		leftRow = "  " + dimmed + "│" + reset + " " + keyColor + fmt.Sprintf("%-*s", colKey-1, "q") + reset + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colDesc-1, "Quit application") + dimmed + "│" + reset
		helpLines = append(helpLines, padLeft(leftRow, leftVisualWidth))

		// === ROW 21: bottom of GENERAL / empty ===
		leftRow = "  " + dimmed + "╰" + strings.Repeat("─", colKey) + "┴" + strings.Repeat("─", colDesc) + "╯" + reset
		helpLines = append(helpLines, padLeft(leftRow, leftVisualWidth))

		// === ROW 22: empty ===
		helpLines = append(helpLines, "")

		// === ROW 23: STATUS COLORS header / SERVICE ACTIONS header ===
		colColor := 10
		colMeaning := 22
		statusColorsWidth := 2 + 1 + colColor + 1 + colMeaning + 1
		extraPad := leftVisualWidth - statusColorsWidth

		leftRow = "  " + bold + cyan + "STATUS COLORS" + reset
		rightRow = bold + cyan + "SERVICE ACTIONS" + reset + "  " + dimmed + "(services view)" + reset
		helpLines = append(helpLines, leftRow+strings.Repeat(" ", statusColorsWidth-15+extraPad)+gap+rightRow)

		// === ROW 24: STATUS COLORS top border / SERVICE ACTIONS top border ===
		leftRow = "  " + dimmed + "╭" + strings.Repeat("─", colColor) + "┬" + strings.Repeat("─", colMeaning) + "╮" + reset
		rightRow = dimmed + "╭" + strings.Repeat("─", colKey) + "┬" + strings.Repeat("─", colDesc) + "╮" + reset
		helpLines = append(helpLines, leftRow+strings.Repeat(" ", extraPad)+gap+rightRow)

		// === ROW 25: Column headers ===
		leftRow = "  " + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colColor-1, "Color") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colMeaning-1, "Meaning") + reset + dimmed + "│" + reset
		rightRow = dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colKey-1, "Key") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colDesc-1, "Action") + reset + dimmed + "│" + reset
		helpLines = append(helpLines, leftRow+strings.Repeat(" ", extraPad)+gap+rightRow)

		// === ROW 26: Separators ===
		leftRow = "  " + dimmed + "├" + strings.Repeat("─", colColor) + "┼" + strings.Repeat("─", colMeaning) + "┤" + reset
		rightRow = dimmed + "├" + strings.Repeat("─", colKey) + "┼" + strings.Repeat("─", colDesc) + "┤" + reset
		helpLines = append(helpLines, leftRow+strings.Repeat(" ", extraPad)+gap+rightRow)

		// === ROW 27: Green / ↑ ↓ ===
		leftRow = "  " + dimmed + "│" + reset + " " + green + "●" + reset + fmt.Sprintf(" %-*s", colColor-3, "Green") + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colMeaning-1, "Running / Ready") + dimmed + "│" + reset
		rightRow = dimmed + "│" + reset + " " + keyColor + fmt.Sprintf("%-*s", colKey-1, "↑ ↓") + reset + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colDesc-1, "Navigate services") + dimmed + "│" + reset
		helpLines = append(helpLines, leftRow+strings.Repeat(" ", extraPad)+gap+rightRow)

		// === ROW 28: Yellow / bottom of SERVICE ACTIONS ===
		leftRow = "  " + dimmed + "│" + reset + " " + yellow + "●" + reset + fmt.Sprintf(" %-*s", colColor-3, "Yellow") + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colMeaning-1, "Pending") + dimmed + "│" + reset
		rightRow = dimmed + "╰" + strings.Repeat("─", colKey) + "┴" + strings.Repeat("─", colDesc) + "╯" + reset
		helpLines = append(helpLines, leftRow+strings.Repeat(" ", extraPad)+gap+rightRow)

		// === ROW 29: Red / empty ===
		leftRow = "  " + dimmed + "│" + reset + " " + red + "●" + reset + fmt.Sprintf(" %-*s", colColor-3, "Red") + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colMeaning-1, "Dead / Failed") + dimmed + "│" + reset
		helpLines = append(helpLines, leftRow)

		// === ROW 30: bottom of STATUS COLORS ===
		leftRow = "  " + dimmed + "╰" + strings.Repeat("─", colColor) + "┴" + strings.Repeat("─", colMeaning) + "╯" + reset
		helpLines = append(helpLines, leftRow)

		// === ROW 31: empty for spacing ===
		helpLines = append(helpLines, "")

		// === ROW 32: ALLOCATION ACTIONS header ===
		helpLines = append(helpLines, "  "+bold+cyan+"ALLOCATION ACTIONS"+reset+"  "+dimmed+"(job-status view, press 'a' to enter alloc mode)"+reset)

		// === ROW 33: ALLOCATION ACTIONS top border ===
		helpLines = append(helpLines, "  "+dimmed+"╭"+strings.Repeat("─", colKey)+"┬"+strings.Repeat("─", colDesc)+"╮"+reset)

		// === ROW 34: Column headers ===
		helpLines = append(helpLines, "  "+dimmed+"│"+reset+" "+bold+fmt.Sprintf("%-*s", colKey-1, "Key")+reset+dimmed+"│"+reset+" "+bold+fmt.Sprintf("%-*s", colDesc-1, "Action")+reset+dimmed+"│"+reset)

		// === ROW 35: Separator ===
		helpLines = append(helpLines, "  "+dimmed+"├"+strings.Repeat("─", colKey)+"┼"+strings.Repeat("─", colDesc)+"┤"+reset)

		// === ROW 36: a ===
		helpLines = append(helpLines, "  "+dimmed+"│"+reset+" "+keyColor+fmt.Sprintf("%-*s", colKey-1, "a")+reset+dimmed+"│"+reset+fmt.Sprintf(" %-*s", colDesc-1, "Toggle alloc select mode")+dimmed+"│"+reset)

		// === ROW 37: ↑ ↓ ===
		helpLines = append(helpLines, "  "+dimmed+"│"+reset+" "+keyColor+fmt.Sprintf("%-*s", colKey-1, "↑ ↓")+reset+dimmed+"│"+reset+fmt.Sprintf(" %-*s", colDesc-1, "Navigate allocations")+dimmed+"│"+reset)

		// === ROW 38: s ===
		helpLines = append(helpLines, "  "+dimmed+"│"+reset+" "+keyColor+fmt.Sprintf("%-*s", colKey-1, "s")+reset+dimmed+"│"+reset+fmt.Sprintf(" %-*s", colDesc-1, "Stop selected allocation")+dimmed+"│"+reset)

		// === ROW 39: x ===
		helpLines = append(helpLines, "  "+dimmed+"│"+reset+" "+keyColor+fmt.Sprintf("%-*s", colKey-1, "x")+reset+dimmed+"│"+reset+fmt.Sprintf(" %-*s", colDesc-1, "Restart selected allocation")+dimmed+"│"+reset)

		// === ROW 40: l ===
		helpLines = append(helpLines, "  "+dimmed+"│"+reset+" "+keyColor+fmt.Sprintf("%-*s", colKey-1, "l")+reset+dimmed+"│"+reset+fmt.Sprintf(" %-*s", colDesc-1, "View allocation logs")+dimmed+"│"+reset)

		// === ROW 41: bottom border ===
		helpLines = append(helpLines, "  "+dimmed+"╰"+strings.Repeat("─", colKey)+"┴"+strings.Repeat("─", colDesc)+"╯"+reset)

		// Build header (no leading newline to avoid cutting off top border)
		header := renderHeader("❓ KEYBOARD SHORTCUTS", boxWidth, m.theme.Header, bold, reset) + "\n"

		// Build footer
		footerHint := "  " + dimmed + "↑↓" + reset + " Scroll  " + dimmed + "│" + reset + "  " + cyan + "h" + reset + "/" + cyan + "Esc" + reset + " Close Help"
		plainFooter := "Press 'h' for help, 'q' for quit"
		footerLen := len(plainFooter)
		leftPad := (m.width - footerLen) / 2
		if leftPad < 0 {
			leftPad = 0
		}
		rightPad := m.width - leftPad - footerLen
		if rightPad < 0 {
			rightPad = 0
		}
		footerBar := "\033[48;5;237m" + "\033[37m" + strings.Repeat(" ", leftPad) + "Press '\033[38;5;51mh\033[37m' for help, '\033[38;5;204mq\033[37m' for quit" + strings.Repeat(" ", rightPad) + "\033[0m"

		// Calculate visible content area
		// Header takes ~5 lines, footer hint + separator + footer bar = 3 lines
		headerLines := strings.Count(header, "\n")
		footerLines := 3
		visibleContentLines := m.height - headerLines - footerLines
		if visibleContentLines < 1 {
			visibleContentLines = 1
		}

		// Clamp scroll offset
		maxScroll := len(helpLines) - visibleContentLines
		if maxScroll < 0 {
			maxScroll = 0
		}
		if m.helpScrollOffset > maxScroll {
			m.helpScrollOffset = maxScroll
		}
		if m.helpScrollOffset < 0 {
			m.helpScrollOffset = 0
		}

		// Get visible slice of help content
		endLine := m.helpScrollOffset + visibleContentLines
		if endLine > len(helpLines) {
			endLine = len(helpLines)
		}
		visibleHelp := helpLines[m.helpScrollOffset:endLine]

		// Build final output
		result := header
		result += strings.Join(visibleHelp, "\n") + "\n"
		result += "\n" + footerHint + "\n"
		result += strings.Repeat("─", m.width) + "\n"
		result += footerBar + "\n"

		return result
	}
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n", m.err)
	}

	// Common styling
	white := "\033[37m"

	switch m.view {
	case "jobs":
		// Header
		content = "\n" + renderHeader("📋 NOMAD JOBS", boxWidth, m.theme.Header, bold, reset) + "\n"

		// Summary bar
		runningCount := 0
		pendingCount := 0
		deadCount := 0
		for _, job := range m.jobs {
			switch job.Status {
			case "running":
				runningCount++
			case "pending":
				pendingCount++
			case "dead":
				deadCount++
			}
		}
		content += "  " + dimmed + "Total:" + reset + " " + bold + fmt.Sprintf("%d", len(m.jobs)) + reset
		content += "  │  " + m.theme.Running + "●" + reset + " Running: " + bold + fmt.Sprintf("%d", runningCount) + reset
		content += "  │  " + m.theme.Pending + "●" + reset + " Pending: " + bold + fmt.Sprintf("%d", pendingCount) + reset
		content += "  │  " + m.theme.Dead + "●" + reset + " Dead: " + bold + fmt.Sprintf("%d", deadCount) + reset + "\n\n"

		// Calculate dynamic column widths based on terminal width
		// Jobs table has 5 columns: Name, Status, Type, Pool, Uptime
		// Weights: Name(4), Status(2), Type(1), Pool(2), Uptime(2) = 11 total parts
		tableWidth := getTableWidth(m.width, 5)
		jobColWidths := calculateColumnWidths(tableWidth, []int{4, 2, 1, 2, 2}, []int{12, 10, 6, 8, 10})
		colName := jobColWidths[0]
		colStatus := jobColWidths[1]
		colType := jobColWidths[2]
		colPool := jobColWidths[3]
		colUptime := jobColWidths[4]

		// Table header with rounded corners
		content += "  " + dimmed + "╭" + strings.Repeat("─", colName) + "┬" + strings.Repeat("─", colStatus) + "┬" + strings.Repeat("─", colType) + "┬" + strings.Repeat("─", colPool) + "┬" + strings.Repeat("─", colUptime) + "╮" + reset + "\n"
		content += "  " + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colName-1, "Job Name") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colStatus-1, "Status") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colType-1, "Type") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colPool-1, "Node Pool") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colUptime-1, "Uptime") + reset + dimmed + "│" + reset + "\n"
		content += "  " + dimmed + "├" + strings.Repeat("─", colName) + "┼" + strings.Repeat("─", colStatus) + "┼" + strings.Repeat("─", colType) + "┼" + strings.Repeat("─", colPool) + "┼" + strings.Repeat("─", colUptime) + "┤" + reset + "\n"

		// Calculate how many jobs can be displayed
		// Chrome lines breakdown:
		// Before table: 1 (leading \n) + 4 (header box + \n) + 2 (summary + blank) + 3 (table header) = 10
		// After table: 1 (table bottom) + 2 (nav hint) + 1 (separator) + 1 (footer) = 5
		// Total chrome = 15 lines
		chromeLines := 15
		maxVisibleJobs := len(m.jobs)
		if m.height > 0 {
			maxVisibleJobs = m.height - chromeLines
			if maxVisibleJobs < 1 {
				maxVisibleJobs = 1
			}
		}

		// Clamp scroll offset
		maxScroll := len(m.jobs) - maxVisibleJobs
		if maxScroll < 0 {
			maxScroll = 0
		}
		if m.jobsScrollOffset > maxScroll {
			m.jobsScrollOffset = maxScroll
		}
		if m.jobsScrollOffset < 0 {
			m.jobsScrollOffset = 0
		}

		// Calculate end index for visible jobs
		endIndex := m.jobsScrollOffset + maxVisibleJobs
		if endIndex > len(m.jobs) {
			endIndex = len(m.jobs)
		}

		for i := m.jobsScrollOffset; i < endIndex; i++ {
			job := m.jobs[i]

			// Job name with selection highlight
			name := truncate(job.Name, colName-2)
			var nameField string
			if i == m.selectedIndex {
				nameField = bold + m.theme.HighlightBg + "\033[30m" + fmt.Sprintf(" %-*s", colName-1, name) + reset
			} else {
				nameField = " " + white + fmt.Sprintf("%-*s", colName-1, name) + reset
			}

			// Status with color and icon
			statusIcon := "○"
			switch job.Status {
			case "running":
				statusIcon = "●"
			case "pending":
				statusIcon = "◐"
			}
			// Add placement failure indicator
			placementIndicator := ""
			if job.hasPlacementIssues {
				placementIndicator = " ⚠"
			}
			statusText := fmt.Sprintf("%s %s%s", statusIcon, job.Status, placementIndicator)
			statusField := " " + ansiColor(job.Status, m.theme) + fmt.Sprintf("%-*s", colStatus-1, statusText) + reset

			// Type
			typeField := " " + fmt.Sprintf("%-*s", colType-1, truncate(job.Type, colType-2))

			// Node Pool
			nodePoolField := " " + fmt.Sprintf("%-*s", colPool-1, truncate(job.nodePool, colPool-2))

			// Uptime
			duration := time.Since(time.Unix(0, job.SubmitTime))
			durationStr := fmt.Sprintf("%dd %dh %dm", int(duration.Hours()/24), int(duration.Hours())%24, int(duration.Minutes())%60)
			durationField := " " + fmt.Sprintf("%-*s", colUptime-1, durationStr)

			content += "  " + dimmed + "│" + reset + nameField + dimmed + "│" + reset + statusField + dimmed + "│" + reset + typeField + dimmed + "│" + reset + nodePoolField + dimmed + "│" + reset + durationField + dimmed + "│" + reset + "\n"
		}

		content += "  " + dimmed + "╰" + strings.Repeat("─", colName) + "┴" + strings.Repeat("─", colStatus) + "┴" + strings.Repeat("─", colType) + "┴" + strings.Repeat("─", colPool) + "┴" + strings.Repeat("─", colUptime) + "╯" + reset + "\n"

		// Scroll indicator (if list is scrollable)
		scrollIndicator := ""
		if len(m.jobs) > maxVisibleJobs {
			scrollIndicator = fmt.Sprintf("  %s(%d-%d of %d)%s", dimmed, m.jobsScrollOffset+1, endIndex, len(m.jobs), reset)
		}

		// Navigation hint
		content += "\n  " + dimmed + "↑↓" + reset + " Navigate  " + dimmed + "│" + reset + "  " + dimmed + "←→" + reset + " Switch View  " + dimmed + "│" + reset + "  " + cyan + "Enter" + reset + " Details  " + dimmed + "│" + reset + "  " + cyan + "s" + reset + " Stop  " + dimmed + "│" + reset + "  " + cyan + "d" + reset + " Delete  " + scrollIndicator + "\n"

	case "nodes":
		// Header
		content = "\n" + renderHeader("🖥️  NOMAD NODES", boxWidth, m.theme.Header, bold, reset) + "\n"

		// Summary bar
		readyCount := 0
		downCount := 0
		for _, node := range m.nodes {
			if node.Status == "ready" {
				readyCount++
			} else {
				downCount++
			}
		}
		content += "  " + dimmed + "Total:" + reset + " " + bold + fmt.Sprintf("%d", len(m.nodes)) + reset
		content += "  │  " + m.theme.Running + "●" + reset + " Ready: " + bold + fmt.Sprintf("%d", readyCount) + reset
		content += "  │  " + m.theme.Dead + "●" + reset + " Down: " + bold + fmt.Sprintf("%d", downCount) + reset + "\n\n"

		// Calculate dynamic column widths based on terminal width
		// Nodes table has 7 columns: ID, Name, DC, OS, Status, Version, IP
		// Weights: ID(1), Name(3), DC(1), OS(2), Status(1), Version(2), IP(2) = 12 total parts
		tableWidth := getTableWidth(m.width, 7)
		nodeColWidths := calculateColumnWidths(tableWidth, []int{1, 3, 1, 2, 1, 2, 2}, []int{8, 12, 6, 10, 8, 10, 12})
		colID := nodeColWidths[0]
		colName := nodeColWidths[1]
		colDC := nodeColWidths[2]
		colOS := nodeColWidths[3]
		colStatus := nodeColWidths[4]
		colVersion := nodeColWidths[5]
		colIP := nodeColWidths[6]

		// Table header with rounded corners
		content += "  " + dimmed + "╭" + strings.Repeat("─", colID) + "┬" + strings.Repeat("─", colName) + "┬" + strings.Repeat("─", colDC) + "┬" + strings.Repeat("─", colOS) + "┬" + strings.Repeat("─", colStatus) + "┬" + strings.Repeat("─", colVersion) + "┬" + strings.Repeat("─", colIP) + "╮" + reset + "\n"
		content += "  " + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colID-1, "ID") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colName-1, "Name") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colDC-1, "DC") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colOS-1, "OS") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colStatus-1, "Status") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colVersion-1, "Version") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colIP-1, "IP") + reset + dimmed + "│" + reset + "\n"
		content += "  " + dimmed + "├" + strings.Repeat("─", colID) + "┼" + strings.Repeat("─", colName) + "┼" + strings.Repeat("─", colDC) + "┼" + strings.Repeat("─", colOS) + "┼" + strings.Repeat("─", colStatus) + "┼" + strings.Repeat("─", colVersion) + "┼" + strings.Repeat("─", colIP) + "┤" + reset + "\n"

		// Calculate how many nodes can be displayed
		// Chrome lines breakdown: same as jobs = 15 lines
		chromeLines := 15
		maxVisibleNodes := len(m.nodes)
		if m.height > 0 {
			maxVisibleNodes = m.height - chromeLines
			if maxVisibleNodes < 1 {
				maxVisibleNodes = 1
			}
		}

		// Clamp scroll offset
		maxScroll := len(m.nodes) - maxVisibleNodes
		if maxScroll < 0 {
			maxScroll = 0
		}
		if m.nodesScrollOffset > maxScroll {
			m.nodesScrollOffset = maxScroll
		}
		if m.nodesScrollOffset < 0 {
			m.nodesScrollOffset = 0
		}

		// Calculate end index for visible nodes
		endIndex := m.nodesScrollOffset + maxVisibleNodes
		if endIndex > len(m.nodes) {
			endIndex = len(m.nodes)
		}

		for i := m.nodesScrollOffset; i < endIndex; i++ {
			node := m.nodes[i]

			// ID (first part)
			idParts := strings.Split(node.ID, "-")
			idText := node.ID
			if len(idParts) > 0 {
				idText = idParts[0]
			}
			idText = truncate(idText, colID-2)
			var idField string
			if i == m.selectedNodeIndex {
				idField = bold + m.theme.HighlightBg + "\033[30m" + fmt.Sprintf(" %-*s", colID-1, idText) + reset
			} else {
				idField = " " + fmt.Sprintf("%-*s", colID-1, idText)
			}

			// DC
			dcField := " " + fmt.Sprintf("%-*s", colDC-1, truncate(node.datacenter, colDC-2))

			// Name
			nameField := " " + fmt.Sprintf("%-*s", colName-1, truncate(node.Name, colName-2))

			// OS with version and color
			osFullText := node.osName
			if node.osVersion != "" {
				osFullText = node.osName + " " + node.osVersion
			}
			osText := truncate(osFullText, colOS-2)
			osColor := ""
			if strings.ToLower(node.osName) == "ubuntu" {
				osColor = "\033[33m"
			} else if strings.ToLower(node.osName) == "rhel" {
				osColor = "\033[31m"
			}
			osField := " " + osColor + fmt.Sprintf("%-*s", colOS-1, osText) + reset

			// Status with icon
			statusIcon := "●"
			if node.Status != "ready" {
				statusIcon = "○"
			}
			statusText := fmt.Sprintf("%s %s", statusIcon, truncate(node.Status, colStatus-4))
			statusField := " " + ansiColor(node.Status, m.theme) + fmt.Sprintf("%-*s", colStatus-1, statusText) + reset

			// Version
			versionField := " " + fmt.Sprintf("%-*s", colVersion-1, truncate(node.version, colVersion-2))

			// IP
			ipField := " " + fmt.Sprintf("%-*s", colIP-1, truncate(node.ip, colIP-2))

			content += "  " + dimmed + "│" + reset + idField + dimmed + "│" + reset + nameField + dimmed + "│" + reset + dcField + dimmed + "│" + reset + osField + dimmed + "│" + reset + statusField + dimmed + "│" + reset + versionField + dimmed + "│" + reset + ipField + dimmed + "│" + reset + "\n"
		}

		content += "  " + dimmed + "╰" + strings.Repeat("─", colID) + "┴" + strings.Repeat("─", colName) + "┴" + strings.Repeat("─", colDC) + "┴" + strings.Repeat("─", colOS) + "┴" + strings.Repeat("─", colStatus) + "┴" + strings.Repeat("─", colVersion) + "┴" + strings.Repeat("─", colIP) + "╯" + reset + "\n"

		// Scroll indicator (if list is scrollable)
		scrollIndicator := ""
		if len(m.nodes) > maxVisibleNodes {
			scrollIndicator = fmt.Sprintf("  %s(%d-%d of %d)%s", dimmed, m.nodesScrollOffset+1, endIndex, len(m.nodes), reset)
		}

		// Navigation hint
		content += "\n  " + dimmed + "↑↓" + reset + " Navigate  " + dimmed + "│" + reset + "  " + dimmed + "←→" + reset + " Switch View  " + dimmed + "│" + reset + "  " + cyan + "Enter" + reset + " Details  " + scrollIndicator + "\n"

	case "cluster":
		// Header
		content = "\n" + renderHeader("🌐 CLUSTER OVERVIEW", boxWidth, m.theme.Header, bold, reset) + "\n"

		// Cluster stats summary
		readyNodes := 0
		downNodes := 0
		for _, node := range m.nodes {
			if node.Status == "ready" {
				readyNodes++
			} else {
				downNodes++
			}
		}
		runningJobs := 0
		pendingJobs := 0
		failedJobs := 0
		for _, job := range m.jobs {
			switch job.Status {
			case "running":
				runningJobs++
			case "pending":
				pendingJobs++
			case "dead":
				failedJobs++
			}
		}

		// Calculate dynamic column widths for cluster status table
		// 3 columns: Label, Value, Status
		clusterTableWidth := getTableWidth(m.width, 3)
		clusterColWidths := calculateColumnWidths(clusterTableWidth, []int{2, 1, 3}, []int{10, 8, 18})
		colLabel := clusterColWidths[0]
		colValue := clusterColWidths[1]
		colStatus := clusterColWidths[2]

		content += "  " + bold + cyan + "CLUSTER STATUS" + reset + "\n"
		content += "  " + dimmed + "╭" + strings.Repeat("─", colLabel) + "┬" + strings.Repeat("─", colValue) + "┬" + strings.Repeat("─", colStatus) + "╮" + reset + "\n"
		content += "  " + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colLabel-1, "Resource") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colValue-1, "Total") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colStatus-1, "Status") + reset + dimmed + "│" + reset + "\n"
		content += "  " + dimmed + "├" + strings.Repeat("─", colLabel) + "┼" + strings.Repeat("─", colValue) + "┼" + strings.Repeat("─", colStatus) + "┤" + reset + "\n"

		// Nodes row
		nodesStatus := m.theme.Running + "●" + reset + " " + fmt.Sprintf("%d ready", readyNodes)
		if downNodes > 0 {
			nodesStatus += "  " + m.theme.Dead + "●" + reset + " " + fmt.Sprintf("%d down", downNodes)
		}
		nodesPadding := colStatus - 1 - (1 + 1 + len(fmt.Sprintf("%d ready", readyNodes)))
		if downNodes > 0 {
			nodesPadding = colStatus - 1 - (1 + 1 + len(fmt.Sprintf("%d ready", readyNodes)) + 2 + 1 + 1 + len(fmt.Sprintf("%d down", downNodes)))
		}
		if nodesPadding < 0 {
			nodesPadding = 0
		}
		content += "  " + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colLabel-1, "Nodes") + dimmed + "│" + reset + fmt.Sprintf(" %-*d", colValue-1, len(m.nodes)) + dimmed + "│" + reset + " " + nodesStatus + strings.Repeat(" ", nodesPadding) + dimmed + "│" + reset + "\n"

		// Jobs row
		jobsStatus := m.theme.Running + "●" + reset + " " + fmt.Sprintf("%d run", runningJobs)
		jobsVisualLen := 1 + 1 + len(fmt.Sprintf("%d run", runningJobs)) // icon(1) + space(1) + text
		if pendingJobs > 0 {
			jobsStatus += "  " + m.theme.Pending + "●" + reset + " " + fmt.Sprintf("%d pend", pendingJobs)
			jobsVisualLen += 2 + 1 + 1 + len(fmt.Sprintf("%d pend", pendingJobs)) // 2 spaces + icon(1) + space(1) + text
		}
		if failedJobs > 0 {
			jobsStatus += "  " + m.theme.Dead + "●" + reset + " " + fmt.Sprintf("%d dead", failedJobs)
			jobsVisualLen += 2 + 1 + 1 + len(fmt.Sprintf("%d dead", failedJobs)) // 2 spaces + icon(1) + space(1) + text
		}
		jobsPadding := colStatus - 1 - jobsVisualLen
		if jobsPadding < 0 {
			jobsPadding = 0
		}
		content += "  " + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colLabel-1, "Jobs") + dimmed + "│" + reset + fmt.Sprintf(" %-*d", colValue-1, len(m.jobs)) + dimmed + "│" + reset + " " + jobsStatus + strings.Repeat(" ", jobsPadding) + dimmed + "│" + reset + "\n"

		content += "  " + dimmed + "╰" + strings.Repeat("─", colLabel) + "┴" + strings.Repeat("─", colValue) + "┴" + strings.Repeat("─", colStatus) + "╯" + reset + "\n\n"

		// CPU Section
		capacityGHz := float64(m.totalCapacityCPU) / 1000
		allocatedGHz := float64(m.totalReservedCPU) / 1000
		availableGHz := capacityGHz - allocatedGHz
		utilCPU := 0.0
		if capacityGHz > 0 {
			utilCPU = (allocatedGHz / capacityGHz) * 100
		}
		cpuColor := m.theme.UtilLow
		if utilCPU > 80 {
			cpuColor = m.theme.UtilHigh
		} else if utilCPU > 50 {
			cpuColor = m.theme.UtilMedium
		}

		// Memory Section calculations
		capacityGB := float64(m.totalCapacityMem) / 1000
		allocatedGB := float64(m.totalReservedMem) / 1024
		availableGB := capacityGB - allocatedGB
		utilMem := 0.0
		if capacityGB > 0 {
			utilMem = (allocatedGB / capacityGB) * 100
		}
		memColor := m.theme.UtilLow
		if utilMem > 80 {
			memColor = m.theme.UtilHigh
		} else if utilMem > 50 {
			memColor = m.theme.UtilMedium
		}

		// Calculate dynamic column widths for resource utilization table
		// 5 columns: Resource, Capacity, Allocated, Available, Util
		resTableWidth := getTableWidth(m.width, 5)
		resColWidths := calculateColumnWidths(resTableWidth, []int{2, 2, 2, 2, 1}, []int{8, 10, 10, 10, 8})
		colResource := resColWidths[0]
		colCapacity := resColWidths[1]
		colAllocated := resColWidths[2]
		colAvailable := resColWidths[3]
		colUtil := resColWidths[4]

		content += "  " + bold + cyan + "RESOURCE UTILIZATION" + reset + "\n"
		content += "  " + dimmed + "╭" + strings.Repeat("─", colResource) + "┬" + strings.Repeat("─", colCapacity) + "┬" + strings.Repeat("─", colAllocated) + "┬" + strings.Repeat("─", colAvailable) + "┬" + strings.Repeat("─", colUtil) + "╮" + reset + "\n"
		content += "  " + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colResource-1, "Resource") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colCapacity-1, "Capacity") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colAllocated-1, "Allocated") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colAvailable-1, "Available") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colUtil-1, "Used") + reset + dimmed + "│" + reset + "\n"
		content += "  " + dimmed + "├" + strings.Repeat("─", colResource) + "┼" + strings.Repeat("─", colCapacity) + "┼" + strings.Repeat("─", colAllocated) + "┼" + strings.Repeat("─", colAvailable) + "┼" + strings.Repeat("─", colUtil) + "┤" + reset + "\n"

		// CPU row
		cpuUtilStr := fmt.Sprintf("%.1f%%", utilCPU)
		content += "  " + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colResource-1, "CPU") + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colCapacity-1, fmt.Sprintf("%.1f GHz", capacityGHz)) + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colAllocated-1, fmt.Sprintf("%.1f GHz", allocatedGHz)) + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colAvailable-1, fmt.Sprintf("%.1f GHz", availableGHz)) + dimmed + "│" + reset + " " + cpuColor + fmt.Sprintf("%-*s", colUtil-1, cpuUtilStr) + reset + dimmed + "│" + reset + "\n"

		// Memory row
		memUtilStr := fmt.Sprintf("%.1f%%", utilMem)
		content += "  " + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colResource-1, "Memory") + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colCapacity-1, fmt.Sprintf("%.1f GB", capacityGB)) + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colAllocated-1, fmt.Sprintf("%.1f GB", allocatedGB)) + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colAvailable-1, fmt.Sprintf("%.1f GB", availableGB)) + dimmed + "│" + reset + " " + memColor + fmt.Sprintf("%-*s", colUtil-1, memUtilStr) + reset + dimmed + "│" + reset + "\n"

		content += "  " + dimmed + "╰" + strings.Repeat("─", colResource) + "┴" + strings.Repeat("─", colCapacity) + "┴" + strings.Repeat("─", colAllocated) + "┴" + strings.Repeat("─", colAvailable) + "┴" + strings.Repeat("─", colUtil) + "╯" + reset + "\n\n"

		// Progress bars section - dynamic width based on terminal
		barWidth := m.width - 20 // Leave room for label and percentage
		if barWidth < 20 {
			barWidth = 20
		}
		if barWidth > 60 {
			barWidth = 60
		}

		content += "  " + bold + cyan + "UTILIZATION" + reset + "\n"

		// CPU Progress bar
		filledWidth := int(utilCPU / 100 * float64(barWidth))
		if filledWidth > barWidth {
			filledWidth = barWidth
		}
		cpuBar := cpuColor + strings.Repeat("█", filledWidth) + reset + dimmed + strings.Repeat("░", barWidth-filledWidth) + reset
		content += "  " + dimmed + "CPU" + reset + "     " + cpuBar + " " + cpuColor + fmt.Sprintf("%5.1f%%", utilCPU) + reset + "\n"

		// Memory Progress bar
		memFilledWidth := int(utilMem / 100 * float64(barWidth))
		if memFilledWidth > barWidth {
			memFilledWidth = barWidth
		}
		memBar := memColor + strings.Repeat("█", memFilledWidth) + reset + dimmed + strings.Repeat("░", barWidth-memFilledWidth) + reset
		content += "  " + dimmed + "Memory" + reset + "  " + memBar + " " + memColor + fmt.Sprintf("%5.1f%%", utilMem) + reset + "\n"

		// Navigation hint
		content += "\n  " + dimmed + "↑↓" + reset + " Scroll  " + dimmed + "│" + reset + "  " + dimmed + "←→" + reset + " Switch View  " + dimmed + "│" + reset + "  " + dimmed + "(more content below)" + reset + "\n"

	case "services":
		// Header
		content = "\n" + renderHeader("🔗 NOMAD SERVICES", boxWidth, m.theme.Header, bold, reset) + "\n"

		// Summary bar
		content += "  " + dimmed + "Total:" + reset + " " + bold + fmt.Sprintf("%d", len(m.services)) + reset + " services registered\n\n"

		// Calculate dynamic column widths based on terminal width
		// Services table has 6 columns: Name, Tags, Address, Port, Job, Node
		// Weights: Name(3), Tags(3), Address(2), Port(1), Job(2), Node(1) = 12 total parts
		tableWidth := getTableWidth(m.width, 6)
		svcColWidths := calculateColumnWidths(tableWidth, []int{3, 3, 2, 1, 2, 1}, []int{12, 12, 12, 6, 12, 8})
		colName := svcColWidths[0]
		colTags := svcColWidths[1]
		colAddress := svcColWidths[2]
		colPort := svcColWidths[3]
		colJob := svcColWidths[4]
		colNode := svcColWidths[5]

		// Table header with rounded corners
		content += "  " + dimmed + "╭" + strings.Repeat("─", colName) + "┬" + strings.Repeat("─", colTags) + "┬" + strings.Repeat("─", colAddress) + "┬" + strings.Repeat("─", colPort) + "┬" + strings.Repeat("─", colJob) + "┬" + strings.Repeat("─", colNode) + "╮" + reset + "\n"
		content += "  " + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colName-1, "Service Name") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colTags-1, "Tags") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colAddress-1, "Address") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colPort-1, "Port") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colJob-1, "Job") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colNode-1, "Node") + reset + dimmed + "│" + reset + "\n"
		content += "  " + dimmed + "├" + strings.Repeat("─", colName) + "┼" + strings.Repeat("─", colTags) + "┼" + strings.Repeat("─", colAddress) + "┼" + strings.Repeat("─", colPort) + "┼" + strings.Repeat("─", colJob) + "┼" + strings.Repeat("─", colNode) + "┤" + reset + "\n"

		// Calculate how many services can be displayed
		// Chrome lines breakdown: same as jobs = 15 lines
		chromeLines := 15
		maxVisibleServices := len(m.services)
		if m.height > 0 {
			maxVisibleServices = m.height - chromeLines
			if maxVisibleServices < 1 {
				maxVisibleServices = 1
			}
		}

		// Clamp scroll offset
		maxScroll := len(m.services) - maxVisibleServices
		if maxScroll < 0 {
			maxScroll = 0
		}
		if m.servicesScrollOffset > maxScroll {
			m.servicesScrollOffset = maxScroll
		}
		if m.servicesScrollOffset < 0 {
			m.servicesScrollOffset = 0
		}

		// Calculate end index for visible services
		endIndex := m.servicesScrollOffset + maxVisibleServices
		if endIndex > len(m.services) {
			endIndex = len(m.services)
		}

		if len(m.services) == 0 {
			// Empty state
			emptyMsg := "No services registered"
			emptyPadding := (colName + colTags + colAddress + colPort + colJob + colNode + 5 - len(emptyMsg)) / 2
			content += "  " + dimmed + "│" + reset + strings.Repeat(" ", emptyPadding) + dimmed + emptyMsg + reset + strings.Repeat(" ", colName+colTags+colAddress+colPort+colJob+colNode+5-emptyPadding-len(emptyMsg)) + dimmed + "│" + reset + "\n"
		} else {
			for i := m.servicesScrollOffset; i < endIndex; i++ {
				svc := m.services[i]

				// Service name with selection highlight
				name := truncate(svc.Name, colName-2)
				var nameField string
				if i == m.selectedServiceIndex {
					nameField = bold + m.theme.HighlightBg + "\033[30m" + fmt.Sprintf(" %-*s", colName-1, name) + reset
				} else {
					nameField = " " + white + fmt.Sprintf("%-*s", colName-1, name) + reset
				}

				// Tags (join first few tags)
				tagsStr := ""
				if len(svc.Tags) > 0 {
					tagsStr = strings.Join(svc.Tags, ", ")
				}
				tagsField := " " + fmt.Sprintf("%-*s", colTags-1, truncate(tagsStr, colTags-2))

				// Address
				addressField := " " + fmt.Sprintf("%-*s", colAddress-1, truncate(svc.Address, colAddress-2))

				// Port
				portField := " " + fmt.Sprintf("%-*d", colPort-1, svc.Port)

				// Job
				jobField := " " + fmt.Sprintf("%-*s", colJob-1, truncate(svc.JobID, colJob-2))

				// Node (short ID)
				nodeID := svc.NodeID
				if len(nodeID) > 8 {
					nodeID = nodeID[:8]
				}
				nodeField := " " + fmt.Sprintf("%-*s", colNode-1, nodeID)

				content += "  " + dimmed + "│" + reset + nameField + dimmed + "│" + reset + tagsField + dimmed + "│" + reset + addressField + dimmed + "│" + reset + portField + dimmed + "│" + reset + jobField + dimmed + "│" + reset + nodeField + dimmed + "│" + reset + "\n"
			}
		}

		content += "  " + dimmed + "╰" + strings.Repeat("─", colName) + "┴" + strings.Repeat("─", colTags) + "┴" + strings.Repeat("─", colAddress) + "┴" + strings.Repeat("─", colPort) + "┴" + strings.Repeat("─", colJob) + "┴" + strings.Repeat("─", colNode) + "╯" + reset + "\n"

		// Scroll indicator (if list is scrollable)
		scrollIndicator := ""
		if len(m.services) > maxVisibleServices {
			scrollIndicator = fmt.Sprintf("  %s(%d-%d of %d)%s", dimmed, m.servicesScrollOffset+1, endIndex, len(m.services), reset)
		}

		// Navigation hint
		content += "\n  " + dimmed + "↑↓" + reset + " Navigate  " + dimmed + "│" + reset + "  " + dimmed + "←→" + reset + " Switch View  " + scrollIndicator + "\n"

	case "node-status":
		if m.selectedNodeIndex >= 0 && m.selectedNodeIndex < len(m.nodes) {
			selectedNode := m.nodes[m.selectedNodeIndex]

			// Header
			content = "\n" + renderHeader("🖥️  NODE DETAILS", boxWidth, m.theme.Header, bold, reset) + "\n"

			// Node name and status
			statusIcon := "●"
			if selectedNode.Status != "ready" {
				statusIcon = "○"
			}
			content += "  " + bold + selectedNode.Name + reset + "  " + ansiColor(selectedNode.Status, m.theme) + statusIcon + " " + selectedNode.Status + reset + "\n"
			content += "  " + dimmed + selectedNode.ID + reset + "\n\n"

			// Dynamic separator width based on terminal
			separatorWidth := m.width - 6
			if separatorWidth < 30 {
				separatorWidth = 30
			}
			if separatorWidth > 80 {
				separatorWidth = 80
			}

			// Basic Info Section
			content += "  " + bold + cyan + "BASIC INFORMATION" + reset + "\n"
			content += "  " + dimmed + strings.Repeat("─", separatorWidth) + reset + "\n"
			content += "  " + dimmed + "Datacenter:" + reset + "   " + selectedNode.datacenter + "\n"
			content += "  " + dimmed + "Node Pool:" + reset + "    " + selectedNode.nodePool + "\n"

			osColor := ""
			if strings.ToLower(selectedNode.osName) == "ubuntu" {
				osColor = "\033[33m"
			} else if strings.ToLower(selectedNode.osName) == "rhel" {
				osColor = "\033[31m"
			}
			osDisplay := selectedNode.osName
			if selectedNode.osVersion != "" {
				osDisplay += " " + selectedNode.osVersion
			}
			content += "  " + dimmed + "OS:" + reset + "           " + osColor + osDisplay + reset + "\n"
			content += "  " + dimmed + "Nomad Version:" + reset + " " + selectedNode.version + "\n"
			content += "  " + dimmed + "IP Address:" + reset + "   " + selectedNode.ip + "\n\n"

			// Resources Section
			content += "  " + bold + cyan + "RESOURCES" + reset + "\n"
			content += "  " + dimmed + strings.Repeat("─", separatorWidth) + reset + "\n"

			if selectedNode.fullNode != nil {
				totalCPU := 0
				if selectedNode.fullNode.Resources != nil && selectedNode.fullNode.Resources.CPU != nil {
					totalCPU = *selectedNode.fullNode.Resources.CPU
				} else if totalCPUStr := selectedNode.fullNode.Attributes["cpu.totalcompute"]; totalCPUStr != "" {
					if tc, err := strconv.Atoi(totalCPUStr); err == nil {
						totalCPU = tc
					}
				}
				allocatedCPU := 0
				if selectedNode.fullNode.ReservedResources != nil {
					allocatedCPU = int(selectedNode.fullNode.ReservedResources.Cpu.CpuShares)
				}

				totalMemMB := 0
				if selectedNode.fullNode.Resources != nil && selectedNode.fullNode.Resources.MemoryMB != nil {
					totalMemMB = *selectedNode.fullNode.Resources.MemoryMB
				} else if totalMemStr := selectedNode.fullNode.Attributes["memory.totalbytes"]; totalMemStr != "" {
					if tm, err := strconv.ParseInt(totalMemStr, 10, 64); err == nil {
						totalMemMB = int(tm / 1024 / 1024)
					}
				}
				allocatedMemMB := 0
				if selectedNode.fullNode.ReservedResources != nil {
					allocatedMemMB = int(selectedNode.fullNode.ReservedResources.Memory.MemoryMB)
				}

				if totalCPU > 0 {
					cpuUtil := float64(allocatedCPU) / float64(totalCPU) * 100
					cpuColor := m.theme.UtilLow
					if cpuUtil > 80 {
						cpuColor = m.theme.UtilHigh
					} else if cpuUtil > 50 {
						cpuColor = m.theme.UtilMedium
					}
					// Dynamic bar width based on terminal width
					barWidth := m.width - 30 // Leave room for label and stats
					if barWidth < 15 {
						barWidth = 15
					}
					if barWidth > 40 {
						barWidth = 40
					}
					filledWidth := int(cpuUtil / 100 * float64(barWidth))
					cpuBar := cpuColor + strings.Repeat("█", filledWidth) + reset + dimmed + strings.Repeat("░", barWidth-filledWidth) + reset
					content += "  " + dimmed + "CPU:" + reset + "  " + cpuBar + fmt.Sprintf(" %d / %d MHz", allocatedCPU, totalCPU) + "\n"
				}

				if totalMemMB > 0 {
					memUtil := float64(allocatedMemMB) / float64(totalMemMB) * 100
					memColor := m.theme.UtilLow
					if memUtil > 80 {
						memColor = m.theme.UtilHigh
					} else if memUtil > 50 {
						memColor = m.theme.UtilMedium
					}
					// Dynamic bar width based on terminal width
					barWidth := m.width - 30 // Leave room for label and stats
					if barWidth < 15 {
						barWidth = 15
					}
					if barWidth > 40 {
						barWidth = 40
					}
					filledWidth := int(memUtil / 100 * float64(barWidth))
					memBar := memColor + strings.Repeat("█", filledWidth) + reset + dimmed + strings.Repeat("░", barWidth-filledWidth) + reset
					content += "  " + dimmed + "Mem:" + reset + "  " + memBar + fmt.Sprintf(" %d / %d MB", allocatedMemMB, totalMemMB) + "\n"
				}
			} else {
				content += "  " + dimmed + "Unable to fetch resource details" + reset + "\n"
			}

			content += "\n"

			// Drivers & Volumes Section
			content += "  " + bold + cyan + "CAPABILITIES" + reset + "\n"
			content += "  " + dimmed + strings.Repeat("─", separatorWidth) + reset + "\n"
			content += "  " + dimmed + "Drivers:" + reset + "      "
			if len(selectedNode.drivers) > 0 {
				content += strings.Join(selectedNode.drivers, ", ")
			} else {
				content += dimmed + "none" + reset
			}
			content += "\n"

			content += "  " + dimmed + "Volumes:" + reset + "      "
			if len(selectedNode.hostVolumes) > 0 {
				content += strings.Join(selectedNode.hostVolumes, ", ")
			} else {
				content += dimmed + "none" + reset
			}
			content += "\n"

			content += "  " + dimmed + "Allocations:" + reset + "  " + bold + fmt.Sprintf("%d", selectedNode.allocationCount) + reset + "\n"

			// Navigation hint
			content += "\n  " + dimmed + "↑↓" + reset + " Scroll  " + dimmed + "│" + reset + "  " + cyan + "n" + reset + "/" + cyan + "p" + reset + " Next/Prev  " + dimmed + "│" + reset + "  " + cyan + "Esc" + reset + " Back\n"
		} else {
			content = "\n  No node selected. Press 'Esc' to go back.\n"
		}

	case "job-status":
		if len(m.jobs) == 0 {
			content = "\n  No jobs available. Press 'b' to go back.\n"
		} else if m.selectedJobIndex >= 0 && m.selectedJobIndex < len(m.jobs) {
			selectedJob := m.jobs[m.selectedJobIndex]

			// Header
			content = "\n" + renderHeader("📋 JOB DETAILS", boxWidth, m.theme.Header, bold, reset) + "\n"

			// Job name with status badge
			statusIcon := "●"
			statusLabel := strings.ToUpper(selectedJob.Status)
			switch selectedJob.Status {
			case "running":
				statusIcon = "●"
			case "pending":
				statusIcon = "◐"
			case "dead":
				statusIcon = "○"
			}

			// Large job name header
			content += "  " + bold + white + selectedJob.Name + reset + "  "
			content += ansiColor(selectedJob.Status, m.theme) + statusIcon + " " + statusLabel + reset
			if selectedJob.hasPlacementIssues {
				content += "  " + m.theme.Dead + "⚠ PLACEMENT ISSUES" + reset
			}
			content += "\n"
			content += "  " + dimmed + "ID: " + selectedJob.ID + reset + "\n\n"

			// Calculate total CPU and Memory for this job from allocations
			var totalCPU, totalMem int
			for _, alloc := range selectedJob.allocs {
				if fullAlloc, ok := selectedJob.allocMap[alloc.ID]; ok && fullAlloc.Resources != nil {
					if fullAlloc.Resources.CPU != nil {
						totalCPU += *fullAlloc.Resources.CPU
					}
					if fullAlloc.Resources.MemoryMB != nil {
						totalMem += *fullAlloc.Resources.MemoryMB
					}
				}
			}

			// Calculate uptime
			duration := time.Since(time.Unix(0, selectedJob.SubmitTime))
			durationStr := fmt.Sprintf("%dd %dh %dm", int(duration.Hours()/24), int(duration.Hours())%24, int(duration.Minutes())%60)

			// Info cards in a clean two-column layout with box drawing
			cardWidth := 38
			gap := "  "

			// Helper to create a padded card row
			cardRow := func(label string, value string) string {
				// Format: "  label     value" padded to cardWidth
				// Label is 10 chars, value fills the rest
				labelPadded := fmt.Sprintf("%-10s", label)
				valuePadded := fmt.Sprintf("%-*s", cardWidth-13, truncate(value, cardWidth-13))
				return "  " + dimmed + labelPadded + reset + " " + valuePadded
			}

			// Top border
			content += "  " + dimmed + "╭" + strings.Repeat("─", cardWidth) + "╮" + reset + gap
			content += dimmed + "╭" + strings.Repeat("─", cardWidth) + "╮" + reset + "\n"

			// Card headers
			content += "  " + dimmed + "│" + reset + " " + bold + cyan + "CONFIGURATION" + reset + strings.Repeat(" ", cardWidth-14) + dimmed + "│" + reset + gap
			content += dimmed + "│" + reset + " " + bold + cyan + "RESOURCES" + reset + strings.Repeat(" ", cardWidth-10) + dimmed + "│" + reset + "\n"

			// Separator
			content += "  " + dimmed + "├" + strings.Repeat("─", cardWidth) + "┤" + reset + gap
			content += dimmed + "├" + strings.Repeat("─", cardWidth) + "┤" + reset + "\n"

			// Row 1: Type | CPU
			content += "  " + dimmed + "│" + reset + cardRow("Type", selectedJob.Type) + dimmed + "│" + reset + gap
			content += dimmed + "│" + reset + cardRow("CPU", fmt.Sprintf("%d MHz", totalCPU)) + dimmed + "│" + reset + "\n"

			// Row 2: Node Pool | Memory
			content += "  " + dimmed + "│" + reset + cardRow("Node Pool", selectedJob.nodePool) + dimmed + "│" + reset + gap
			content += dimmed + "│" + reset + cardRow("Memory", fmt.Sprintf("%d MB", totalMem)) + dimmed + "│" + reset + "\n"

			// Row 3: Uptime | (empty padding)
			content += "  " + dimmed + "│" + reset + cardRow("Uptime", durationStr) + dimmed + "│" + reset + gap
			content += dimmed + "│" + reset + strings.Repeat(" ", cardWidth) + dimmed + "│" + reset + "\n"

			// Bottom border
			content += "  " + dimmed + "╰" + strings.Repeat("─", cardWidth) + "╯" + reset + gap
			content += dimmed + "╰" + strings.Repeat("─", cardWidth) + "╯" + reset + "\n\n"

			// Allocations section with summary badges
			runningAllocs := 0
			pendingAllocs := 0
			failedAllocs := 0
			for status, count := range selectedJob.allocStatuses {
				switch status {
				case "running":
					runningAllocs = count
				case "pending":
					pendingAllocs = count
				case "failed":
					failedAllocs = count
				}
			}

			content += "  " + bold + cyan + "ALLOCATIONS" + reset + "  "
			content += dimmed + "(" + reset + m.theme.Running + "●" + reset + " " + fmt.Sprintf("%d", runningAllocs) + " running"
			if pendingAllocs > 0 {
				content += dimmed + " · " + reset + m.theme.Pending + "●" + reset + " " + fmt.Sprintf("%d", pendingAllocs) + " pending"
			}
			if failedAllocs > 0 {
				content += dimmed + " · " + reset + m.theme.Dead + "●" + reset + " " + fmt.Sprintf("%d", failedAllocs) + " failed"
			}
			content += dimmed + ")" + reset + "\n"

			if len(selectedJob.allocs) > 0 {
				// Calculate dynamic column widths for allocations table
				// 5 columns: ID, TaskGroup, Status, Event, Node
				allocTableWidth := getTableWidth(m.width, 5)
				allocColWidths := calculateColumnWidths(allocTableWidth, []int{2, 3, 2, 2, 3}, []int{10, 12, 10, 10, 12})
				colID := allocColWidths[0]
				colTaskGroup := allocColWidths[1]
				colStatus := allocColWidths[2]
				colEvent := allocColWidths[3]
				colNode := allocColWidths[4]

				content += "  " + dimmed + "╭" + strings.Repeat("─", colID) + "┬" + strings.Repeat("─", colTaskGroup) + "┬" + strings.Repeat("─", colStatus) + "┬" + strings.Repeat("─", colEvent) + "┬" + strings.Repeat("─", colNode) + "╮" + reset + "\n"
				content += "  " + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colID-1, "Alloc ID") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colTaskGroup-1, "Task Group") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colStatus-1, "Status") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colEvent-1, "Last Event") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colNode-1, "Node") + reset + dimmed + "│" + reset + "\n"
				content += "  " + dimmed + "├" + strings.Repeat("─", colID) + "┼" + strings.Repeat("─", colTaskGroup) + "┼" + strings.Repeat("─", colStatus) + "┼" + strings.Repeat("─", colEvent) + "┼" + strings.Repeat("─", colNode) + "┤" + reset + "\n"

				maxAllocs := len(selectedJob.allocs)

				for i := 0; i < maxAllocs; i++ {
					alloc := selectedJob.allocs[i]
					allocID := alloc.ID
					if len(allocID) > 12 {
						allocID = allocID[:12]
					}

					status := alloc.ClientStatus
					statusIcon := "●"
					if status != "running" {
						statusIcon = "○"
					}

					nodeName := ""
					if alloc.NodeID != "" {
						nodeName = truncate(alloc.NodeID[:8], 20)
					}

					// Task Group
					taskGroup := truncate(alloc.TaskGroup, colTaskGroup-2)

					// Get last event from full allocation info
					lastEvent := ""
					if fullAlloc, ok := selectedJob.allocMap[alloc.ID]; ok {
						var latestTime int64
						for _, taskState := range fullAlloc.TaskStates {
							if taskState != nil && len(taskState.Events) > 0 {
								for _, event := range taskState.Events {
									if event.Time > latestTime {
										latestTime = event.Time
										lastEvent = event.Type
									}
								}
							}
						}
					}

					// Format fields with proper padding
					idField := fmt.Sprintf(" %-*s", colID-1, allocID)
					taskGroupField := fmt.Sprintf(" %-*s", colTaskGroup-1, taskGroup)
					// Build status field manually for correct visual width
					// colStatus = 12: 1 leading space + 1 icon (visual) + 1 space + status + trailing padding
					// Visual width needed for status + padding = 12 - 3 = 9
					statusText := statusIcon + " " + status
					visualWidth := 1 + 1 + len(status) // icon(1) + space(1) + status
					statusPadding := colStatus - 1 - visualWidth
					if statusPadding < 0 {
						statusPadding = 0
					}
					statusField := " " + statusText + strings.Repeat(" ", statusPadding)
					eventField := fmt.Sprintf(" %-*s", colEvent-1, truncate(lastEvent, colEvent-2))
					nodeField := fmt.Sprintf(" %-*s", colNode-1, nodeName)

					if i == m.selectedAllocIndex {
						content += "  " + dimmed + "│" + reset + bold + m.theme.HighlightBg + "\033[30m" + idField + reset + dimmed + "│" + reset + taskGroupField + dimmed + "│" + reset + ansiColor(status, m.theme) + statusField + reset + dimmed + "│" + reset + eventField + dimmed + "│" + reset + nodeField + dimmed + "│" + reset + "\n"
					} else {
						content += "  " + dimmed + "│" + reset + idField + dimmed + "│" + reset + taskGroupField + dimmed + "│" + reset + ansiColor(status, m.theme) + statusField + reset + dimmed + "│" + reset + eventField + dimmed + "│" + reset + nodeField + dimmed + "│" + reset + "\n"
					}
				}

				content += "  " + dimmed + "╰" + strings.Repeat("─", colID) + "┴" + strings.Repeat("─", colTaskGroup) + "┴" + strings.Repeat("─", colStatus) + "┴" + strings.Repeat("─", colEvent) + "┴" + strings.Repeat("─", colNode) + "╯" + reset + "\n"
			} else {
				content += "  " + dimmed + "No allocations" + reset + "\n"
			}

			// Evaluations section
			content += "\n  " + bold + cyan + "EVALUATIONS" + reset + "\n"

			if len(selectedJob.evaluations) > 0 {
				// Calculate dynamic column widths for evaluations table
				// 5 columns: EvalID, Status, TriggeredBy, Placement, Time
				evalTableWidth := getTableWidth(m.width, 5)
				evalColWidths := calculateColumnWidths(evalTableWidth, []int{2, 2, 2, 3, 2}, []int{10, 10, 12, 14, 14})
				colEvalID := evalColWidths[0]
				colEvalStatus := evalColWidths[1]
				colTriggeredBy := evalColWidths[2]
				colPlacement := evalColWidths[3]
				colTime := evalColWidths[4]

				content += "  " + dimmed + "╭" + strings.Repeat("─", colEvalID) + "┬" + strings.Repeat("─", colEvalStatus) + "┬" + strings.Repeat("─", colTriggeredBy) + "┬" + strings.Repeat("─", colPlacement) + "┬" + strings.Repeat("─", colTime) + "╮" + reset + "\n"
				content += "  " + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colEvalID-1, "Eval ID") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colEvalStatus-1, "Status") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colTriggeredBy-1, "Triggered By") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colPlacement-1, "Placement") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colTime-1, "Time") + reset + dimmed + "│" + reset + "\n"
				content += "  " + dimmed + "├" + strings.Repeat("─", colEvalID) + "┼" + strings.Repeat("─", colEvalStatus) + "┼" + strings.Repeat("─", colTriggeredBy) + "┼" + strings.Repeat("─", colPlacement) + "┼" + strings.Repeat("─", colTime) + "┤" + reset + "\n"

				// Show up to 5 most recent evaluations
				maxEvals := 5
				if len(selectedJob.evaluations) < maxEvals {
					maxEvals = len(selectedJob.evaluations)
				}

				for i := 0; i < maxEvals; i++ {
					eval := selectedJob.evaluations[i]

					// Eval ID (truncated)
					evalID := eval.ID
					if len(evalID) > 12 {
						evalID = evalID[:12]
					}

					// Status with color
					evalStatus := eval.Status
					statusColor := ""
					switch evalStatus {
					case "complete":
						statusColor = m.theme.Running
					case "pending":
						statusColor = m.theme.Pending
					case "blocked", "failed", "canceled":
						statusColor = m.theme.Dead
					}

					// Triggered by
					triggeredBy := truncate(eval.TriggeredBy, colTriggeredBy-2)

					// Placement failures indicator
					placementStatus := "OK"
					placementColor := m.theme.Running
					if eval.FailedTGAllocs != nil && len(eval.FailedTGAllocs) > 0 {
						failedCount := 0
						for _, metric := range eval.FailedTGAllocs {
							failedCount += metric.CoalescedFailures + 1
						}
						placementStatus = fmt.Sprintf("Failed (%d)", failedCount)
						placementColor = m.theme.Dead
					} else if evalStatus == "blocked" {
						placementStatus = "Blocked"
						placementColor = m.theme.Pending
					}

					// Time
					evalTime := time.Unix(0, eval.CreateTime)
					timeStr := evalTime.Format("Jan 02 15:04:05")

					// Format fields
					idField := fmt.Sprintf(" %-*s", colEvalID-1, evalID)
					statusField := fmt.Sprintf(" %-*s", colEvalStatus-1, evalStatus)
					triggeredField := fmt.Sprintf(" %-*s", colTriggeredBy-1, triggeredBy)
					placementField := fmt.Sprintf(" %-*s", colPlacement-1, placementStatus)
					timeField := fmt.Sprintf(" %-*s", colTime-1, timeStr)

					content += "  " + dimmed + "│" + reset + idField + dimmed + "│" + reset + statusColor + statusField + reset + dimmed + "│" + reset + triggeredField + dimmed + "│" + reset + placementColor + placementField + reset + dimmed + "│" + reset + timeField + dimmed + "│" + reset + "\n"
				}

				content += "  " + dimmed + "╰" + strings.Repeat("─", colEvalID) + "┴" + strings.Repeat("─", colEvalStatus) + "┴" + strings.Repeat("─", colTriggeredBy) + "┴" + strings.Repeat("─", colPlacement) + "┴" + strings.Repeat("─", colTime) + "╯" + reset + "\n"
			} else {
				content += "  " + dimmed + "No evaluations" + reset + "\n"
			}

			// Placement failures section
			if selectedJob.hasPlacementIssues && len(selectedJob.placementFailures) > 0 {
				content += "\n  " + bold + m.theme.Dead + "⚠ PLACEMENT FAILURES" + reset + "\n"

				for taskGroup, metrics := range selectedJob.placementFailures {
					content += "  " + dimmed + "╭" + strings.Repeat("─", 76) + "╮" + reset + "\n"
					content += "  " + dimmed + "│" + reset + " " + bold + "Task Group: " + reset + white + taskGroup + reset + strings.Repeat(" ", 76-14-len(taskGroup)) + dimmed + "│" + reset + "\n"
					content += "  " + dimmed + "├" + strings.Repeat("─", 76) + "┤" + reset + "\n"

					// Show evaluation summary
					evalInfo := fmt.Sprintf("Nodes Evaluated: %d  |  Nodes Filtered: %d  |  Nodes Exhausted: %d",
						metrics.NodesEvaluated, metrics.NodesFiltered, metrics.NodesExhausted)
					content += "  " + dimmed + "│" + reset + " " + fmt.Sprintf("%-75s", evalInfo) + dimmed + "│" + reset + "\n"

					// Show constraint failures if any
					if len(metrics.ConstraintFiltered) > 0 {
						content += "  " + dimmed + "│" + reset + " " + m.theme.Dead + "Constraint Failures:" + reset + strings.Repeat(" ", 54) + dimmed + "│" + reset + "\n"
						for constraint, count := range metrics.ConstraintFiltered {
							constraintLine := fmt.Sprintf("  • %s (%d nodes filtered)", truncate(constraint, 60), count)
							content += "  " + dimmed + "│" + reset + " " + fmt.Sprintf("%-75s", constraintLine) + dimmed + "│" + reset + "\n"
						}
					}

					// Show dimension exhausted (resource shortages)
					if len(metrics.DimensionExhausted) > 0 {
						content += "  " + dimmed + "│" + reset + " " + m.theme.Dead + "Resource Exhaustion:" + reset + strings.Repeat(" ", 54) + dimmed + "│" + reset + "\n"
						for dimension, count := range metrics.DimensionExhausted {
							dimLine := fmt.Sprintf("  • %s exhausted on %d nodes", dimension, count)
							content += "  " + dimmed + "│" + reset + " " + fmt.Sprintf("%-75s", dimLine) + dimmed + "│" + reset + "\n"
						}
					}

					// Show class exhausted if any
					if len(metrics.ClassExhausted) > 0 {
						content += "  " + dimmed + "│" + reset + " " + m.theme.Dead + "Node Class Exhausted:" + reset + strings.Repeat(" ", 53) + dimmed + "│" + reset + "\n"
						for class, count := range metrics.ClassExhausted {
							classLine := fmt.Sprintf("  • %s (%d nodes)", class, count)
							content += "  " + dimmed + "│" + reset + " " + fmt.Sprintf("%-75s", classLine) + dimmed + "│" + reset + "\n"
						}
					}

					// Show quota exhausted if any
					if len(metrics.QuotaExhausted) > 0 {
						content += "  " + dimmed + "│" + reset + " " + m.theme.Dead + "Quota Exhausted:" + reset + strings.Repeat(" ", 58) + dimmed + "│" + reset + "\n"
						for _, quota := range metrics.QuotaExhausted {
							quotaLine := fmt.Sprintf("  • %s", quota)
							content += "  " + dimmed + "│" + reset + " " + fmt.Sprintf("%-75s", quotaLine) + dimmed + "│" + reset + "\n"
						}
					}

					// Show coalesced failures count
					if metrics.CoalescedFailures > 0 {
						coalescedLine := fmt.Sprintf("(+%d additional placement failures)", metrics.CoalescedFailures)
						content += "  " + dimmed + "│" + reset + " " + dimmed + fmt.Sprintf("%-75s", coalescedLine) + reset + dimmed + "│" + reset + "\n"
					}

					content += "  " + dimmed + "╰" + strings.Repeat("─", 76) + "╯" + reset + "\n"
				}
			}

			// Clean navigation bar - dynamic based on allocation selection mode
			if m.allocSelectMode {
				content += "\n  " + m.theme.Running + "ALLOC MODE" + reset + "  " + dimmed + "│" + reset + "  " + dimmed + "↑↓" + reset + " Navigate  " + dimmed + "│" + reset + "  " + cyan + "s" + reset + " Stop  " + dimmed + "│" + reset + "  " + cyan + "x" + reset + " Restart  " + dimmed + "│" + reset + "  " + cyan + "l" + reset + " Logs  " + dimmed + "│" + reset + "  " + cyan + "Esc" + reset + " Exit Mode\n"
			} else {
				content += "\n  " + dimmed + "↑↓" + reset + " Scroll  " + dimmed + "│" + reset + "  " + cyan + "a" + reset + " Select Alloc  " + dimmed + "│" + reset + "  " + cyan + "n" + reset + "/" + cyan + "p" + reset + " Next/Prev  " + dimmed + "│" + reset + "  " + cyan + "l" + reset + " Logs  " + dimmed + "│" + reset + "  " + cyan + "e" + reset + " Events  " + dimmed + "│" + reset + "  " + cyan + "Esc" + reset + " Back\n"
			}
		} else {
			content = "\n  Invalid job selection. Press 'Esc' to go back.\n"
		}

	case "job-logs":
		// Header
		content = "\n" + renderHeader("📜 RECENT LOGS", boxWidth, m.theme.Header, bold, reset) + "\n"

		// Job and task info
		content += "  " + bold + m.logJobName + reset + "\n"
		if m.logAllocID != "" {
			allocShort := m.logAllocID
			if len(allocShort) > 12 {
				allocShort = allocShort[:12]
			}
			content += "  " + dimmed + "Allocation:" + reset + " " + allocShort + "  " + dimmed + "Task:" + reset + " " + m.logTaskName + "\n"
		}
		content += "\n"

		// Log content section
		content += "  " + dimmed + strings.Repeat("─", 60) + reset + "\n"

		// Calculate available lines for logs
		maxLogLines := m.height - 12
		if maxLogLines < 5 {
			maxLogLines = 5
		}

		// Split log content into lines and show the last N lines
		logLines := strings.Split(m.logContent, "\n")
		startLine := 0
		if len(logLines) > maxLogLines {
			startLine = len(logLines) - maxLogLines
		}

		for i := startLine; i < len(logLines); i++ {
			line := logLines[i]
			// Highlight STDOUT/STDERR headers
			if strings.HasPrefix(line, "=== STDOUT ===") {
				content += "  " + bold + m.theme.Running + line + reset + "\n"
			} else if strings.HasPrefix(line, "=== STDERR ===") {
				content += "  " + bold + m.theme.Dead + line + reset + "\n"
			} else {
				// Truncate long lines to terminal width
				if len(line) > m.width-4 {
					line = line[:m.width-7] + "..."
				}
				content += "  " + line + "\n"
			}
		}

		// Navigation hint
		content += "\n  " + cyan + "r" + reset + " Refresh  " + dimmed + "│" + reset + "  " + cyan + "Esc" + reset + " Back\n"

	case "job-events":
		// Header
		content = "\n" + renderHeader("📋 JOB EVENTS", boxWidth, m.theme.Header, bold, reset) + "\n"

		// Job info
		content += "  " + bold + m.eventsJobName + reset + "\n\n"

		// Events section
		content += "  " + bold + cyan + "RECENT EVENTS" + reset + "\n"

		if len(m.eventsList) == 0 {
			content += "  " + dimmed + "Loading events..." + reset + "\n"
		} else {
			// Calculate dynamic column widths for events table
			// 5 columns: Time, Alloc, Task, Type, Message
			eventsTableWidth := getTableWidth(m.width, 5)
			eventsColWidths := calculateColumnWidths(eventsTableWidth, []int{3, 1, 2, 2, 4}, []int{16, 8, 10, 10, 16})
			colTime := eventsColWidths[0]
			colAlloc := eventsColWidths[1]
			colTask := eventsColWidths[2]
			colType := eventsColWidths[3]
			colMsg := eventsColWidths[4]

			content += "  " + dimmed + "╭" + strings.Repeat("─", colTime) + "┬" + strings.Repeat("─", colAlloc) + "┬" + strings.Repeat("─", colTask) + "┬" + strings.Repeat("─", colType) + "┬" + strings.Repeat("─", colMsg) + "╮" + reset + "\n"
			content += "  " + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colTime-1, "Time") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colAlloc-1, "Alloc") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colTask-1, "Task") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colType-1, "Type") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colMsg-1, "Message") + reset + dimmed + "│" + reset + "\n"
			content += "  " + dimmed + "├" + strings.Repeat("─", colTime) + "┼" + strings.Repeat("─", colAlloc) + "┼" + strings.Repeat("─", colTask) + "┼" + strings.Repeat("─", colType) + "┼" + strings.Repeat("─", colMsg) + "┤" + reset + "\n"

			// Calculate max events to display
			maxEvents := m.height - 14
			if maxEvents < 5 {
				maxEvents = 5
			}
			if maxEvents > len(m.eventsList) {
				maxEvents = len(m.eventsList)
			}

			for i := 0; i < maxEvents; i++ {
				event := m.eventsList[i]

				timeStr := event.Time.Format("Jan 02 15:04:05")
				timeField := " " + fmt.Sprintf("%-*s", colTime-1, truncate(timeStr, colTime-2))

				allocField := " " + fmt.Sprintf("%-*s", colAlloc-1, truncate(event.AllocID, colAlloc-2))
				taskField := " " + fmt.Sprintf("%-*s", colTask-1, truncate(event.Task, colTask-2))

				// Color-code event types
				typeColor := ""
				switch event.Type {
				case "Started", "Task Setup":
					typeColor = m.theme.Running
				case "Terminated", "Killing", "Killed":
					typeColor = m.theme.Dead
				case "Received", "Pending":
					typeColor = m.theme.Pending
				}
				typeField := " " + typeColor + fmt.Sprintf("%-*s", colType-1, truncate(event.Type, colType-2)) + reset

				msgField := " " + fmt.Sprintf("%-*s", colMsg-1, truncate(event.Message, colMsg-2))

				content += "  " + dimmed + "│" + reset + timeField + dimmed + "│" + reset + allocField + dimmed + "│" + reset + taskField + dimmed + "│" + reset + typeField + dimmed + "│" + reset + msgField + dimmed + "│" + reset + "\n"
			}

			content += "  " + dimmed + "╰" + strings.Repeat("─", colTime) + "┴" + strings.Repeat("─", colAlloc) + "┴" + strings.Repeat("─", colTask) + "┴" + strings.Repeat("─", colType) + "┴" + strings.Repeat("─", colMsg) + "╯" + reset + "\n"
		}

		// Navigation hint
		content += "\n  " + dimmed + "↑↓" + reset + " Navigate  " + dimmed + "│" + reset + "  " + cyan + "r" + reset + " Refresh  " + dimmed + "│" + reset + "  " + cyan + "Esc" + reset + " Back\n"
	}
	// Footer bar with grey background
	plainFooter := "Press 'h' for help, 'q' for quit"
	footerLen := len(plainFooter)
	leftPad := (m.width - footerLen) / 2
	if leftPad < 0 {
		leftPad = 0
	}
	rightPad := m.width - leftPad - footerLen
	if rightPad < 0 {
		rightPad = 0
	}
	footerLine := "\033[48;5;237m" + "\033[37m" + strings.Repeat(" ", leftPad) + "Press '\033[38;5;51mh\033[37m' for help, '\033[38;5;204mq\033[37m' for quit" + strings.Repeat(" ", rightPad) + "\033[0m"
	content += "\n" + strings.Repeat("─", m.width) + "\n" + footerLine + "\n"
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")

	// Apply scrolling for scrollable views (job-status, node-status, cluster)
	// Reserve 4 lines for footers: 1 blank + 1 nav hint + 1 separator + 1 app footer bar
	footerReserve := 4
	scrollableViews := m.view == "job-status" || m.view == "node-status" || m.view == "cluster"
	if scrollableViews && len(lines) > m.height {
		// Calculate visible area (excluding footer lines)
		visibleLines := m.height - footerReserve
		totalScrollable := len(lines) - footerReserve // exclude footers

		// Clamp scroll offset
		maxScroll := totalScrollable - visibleLines
		if maxScroll < 0 {
			maxScroll = 0
		}
		scrollOffset := m.scrollOffset
		if scrollOffset > maxScroll {
			scrollOffset = maxScroll
		}
		if scrollOffset < 0 {
			scrollOffset = 0
		}

		// Get scrolled content + footers
		endLine := scrollOffset + visibleLines
		if endLine > totalScrollable {
			endLine = totalScrollable
		}
		scrolledLines := lines[scrollOffset:endLine]
		// Add footer lines back (page footer + app footer)
		scrolledLines = append(scrolledLines, lines[len(lines)-footerReserve:]...)
		lines = scrolledLines
	} else if len(lines) > m.height {
		// For non-scrollable views, truncate content but keep footers visible
		lines = append(lines[:m.height-footerReserve], lines[len(lines)-footerReserve:]...)
	}
	mainContent := strings.Join(lines, "\n") + "\n"
	if m.confirmAction != "" {
		var message string
		if m.confirmAlloc != nil {
			// Allocation action confirmation
			allocShort := m.confirmAlloc.ID
			if len(allocShort) > 8 {
				allocShort = allocShort[:8]
			}
			actionName := "stop"
			if m.confirmAction == "restart-alloc" {
				actionName = "restart"
			}
			message = fmt.Sprintf("Are you sure you want to %s allocation '%s'?", actionName, allocShort)
		} else if m.confirmJob != nil {
			// Job action confirmation
			action := strings.Title(m.confirmAction)
			message = fmt.Sprintf("Are you sure you want to %s job '%s'?", action, m.confirmJob.Name)
		}

		// Create a centered modal dialog box
		dialogWidth := len(message) + 6
		if dialogWidth < 40 {
			dialogWidth = 40
		}
		if dialogWidth > m.width-4 {
			dialogWidth = m.width - 4
		}

		// Calculate vertical position (center of screen)
		dialogHeight := 7
		startRow := (m.height - dialogHeight) / 2
		if startRow < 0 {
			startRow = 0
		}

		// Calculate horizontal padding
		leftPad := (m.width - dialogWidth) / 2
		if leftPad < 0 {
			leftPad = 0
		}
		padding := strings.Repeat(" ", leftPad)

		// Build dialog box
		topBorder := padding + "╭" + strings.Repeat("─", dialogWidth-2) + "╮"
		bottomBorder := padding + "╰" + strings.Repeat("─", dialogWidth-2) + "╯"
		emptyLine := padding + "│" + strings.Repeat(" ", dialogWidth-2) + "│"

		// Center the message
		msgPadLeft := (dialogWidth - 2 - len(message)) / 2
		msgPadRight := dialogWidth - 2 - len(message) - msgPadLeft
		messageLine := padding + "│" + strings.Repeat(" ", msgPadLeft) + bold + message + reset + strings.Repeat(" ", msgPadRight) + "│"

		// Options line
		options := "[" + m.theme.Running + "y" + reset + "]es  /  [" + m.theme.Dead + "n" + reset + "]o"
		optVisualLen := 12 // "y" + "es  /  " + "n" + "o" + brackets
		optPadLeft := (dialogWidth - 2 - optVisualLen) / 2
		optPadRight := dialogWidth - 2 - optVisualLen - optPadLeft
		optionsLine := padding + "│" + strings.Repeat(" ", optPadLeft) + options + strings.Repeat(" ", optPadRight) + "│"

		// Split main content into lines and overlay the dialog
		contentLines := strings.Split(strings.TrimSuffix(mainContent, "\n"), "\n")

		// Ensure we have enough lines
		for len(contentLines) < m.height {
			contentLines = append(contentLines, "")
		}

		// Overlay dialog onto content
		dialogLines := []string{topBorder, emptyLine, messageLine, emptyLine, optionsLine, emptyLine, bottomBorder}
		for i, dLine := range dialogLines {
			row := startRow + i
			if row >= 0 && row < len(contentLines) {
				contentLines[row] = dLine
			}
		}

		return strings.Join(contentLines, "\n") + "\n"
	}
	return mainContent
}

func runTUI(client *api.Client, theme Theme) {
	os.Setenv("COLORTERM", "truecolor")
	m := model{view: "jobs", client: client, height: 24, width: 80, selectedIndex: 0, showHelp: false, theme: theme, confirmAction: "", confirmJob: nil}
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}

func main() {
	var addr string
	var token string
	var skipVerify bool
	var themeName string

	flag.StringVar(&addr, "addr", "", "Nomad server address")
	flag.StringVar(&token, "token", "", "Nomad ACL token")
	flag.BoolVar(&skipVerify, "skip-verify", false, "Skip TLS certificate verification")
	flag.StringVar(&themeName, "theme", "nomad", "Color theme (nomad, vault, consul, boundary, packer, terraform, waypoint, vagrant)")
	flag.Parse()

	if addr != "" && !strings.Contains(addr, "://") {
		addr = "https://" + addr
	}

	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Println("nomad-tui requires a terminal")
		os.Exit(1)
	}

	config := api.DefaultConfig()
	if addr != "" {
		config.Address = addr
	}
	if token != "" {
		config.SecretID = token
	}
	if skipVerify {
		config.TLSConfig.Insecure = skipVerify
	}

	client, err := api.NewClient(config)
	if err != nil {
		fmt.Printf("Error creating Nomad client: %v\n", err)
		os.Exit(1)
	}

	themes := map[string]Theme{
		"nomad": {
			Running:     "\033[32m",
			Pending:     "\033[33m",
			Dead:        "\033[31m",
			Header:      "\033[1;38;5;42m",
			UtilLow:     "\033[32m",
			UtilMedium:  "\033[33m",
			UtilHigh:    "\033[31m",
			HighlightBg: "\033[48;5;42m",
		},
		"vault": {
			Running:     "\033[32m",
			Pending:     "\033[33m",
			Dead:        "\033[31m",
			Header:      "\033[1;38;5;222m",
			UtilLow:     "\033[32m",
			UtilMedium:  "\033[33m",
			UtilHigh:    "\033[31m",
			HighlightBg: "\033[48;5;222m",
		},
		"consul": {
			Running:     "\033[32m",
			Pending:     "\033[33m",
			Dead:        "\033[31m",
			Header:      "\033[1;38;5;168m",
			UtilLow:     "\033[32m",
			UtilMedium:  "\033[33m",
			UtilHigh:    "\033[31m",
			HighlightBg: "\033[48;5;168m",
		},
		"boundary": {
			Running:     "\033[32m",
			Pending:     "\033[33m",
			Dead:        "\033[31m",
			Header:      "\033[1;38;5;203m",
			UtilLow:     "\033[32m",
			UtilMedium:  "\033[33m",
			UtilHigh:    "\033[31m",
			HighlightBg: "\033[48;5;203m",
		},
		"packer": {
			Running:     "\033[32m",
			Pending:     "\033[33m",
			Dead:        "\033[31m",
			Header:      "\033[1;38;5;75m",
			UtilLow:     "\033[32m",
			UtilMedium:  "\033[33m",
			UtilHigh:    "\033[31m",
			HighlightBg: "\033[48;5;75m",
		},
		"terraform": {
			Running:     "\033[32m",
			Pending:     "\033[33m",
			Dead:        "\033[31m",
			Header:      "\033[1;38;5;141m",
			UtilLow:     "\033[32m",
			UtilMedium:  "\033[33m",
			UtilHigh:    "\033[31m",
			HighlightBg: "\033[48;5;141m",
		},
		"waypoint": {
			Running:     "\033[32m",
			Pending:     "\033[33m",
			Dead:        "\033[31m",
			Header:      "\033[1;38;5;51m",
			UtilLow:     "\033[32m",
			UtilMedium:  "\033[33m",
			UtilHigh:    "\033[31m",
			HighlightBg: "\033[48;5;51m",
		},
		"vagrant": {
			Running:     "\033[32m",
			Pending:     "\033[33m",
			Dead:        "\033[31m",
			Header:      "\033[1;38;5;26m",
			UtilLow:     "\033[32m",
			UtilMedium:  "\033[33m",
			UtilHigh:    "\033[31m",
			HighlightBg: "\033[48;5;26m",
		},
	}

	theme, ok := themes[themeName]
	if !ok {
		fmt.Printf("Unknown theme: %s\n", themeName)
		os.Exit(1)
	}

	runTUI(client, theme)
}
