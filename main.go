package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
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
	confirmJobs          []*jobStats // for multi-job operations (delete multiple jobs)
	confirmAlloc         *api.AllocationListStub
	selectionAnchor      int                 // anchor index for shift+selection range
	selectedJobIDs       map[string]struct{} // tracks multi-selected job IDs
	logContent           string
	logJobName           string
	logAllocID           string
	logTaskName          string
	eventsList           []jobEvent
	eventsJobName        string
	scrollOffset         int             // scroll offset for scrollable views (job-status, node-status, cluster)
	helpScrollOffset     int             // scroll offset for help view
	jobsScrollOffset     int             // scroll offset for jobs list view
	nodesScrollOffset    int             // scroll offset for nodes list view
	servicesScrollOffset int             // scroll offset for services list view
	logsScrollOffset     int             // scroll offset for logs view
	logsFollowMode       bool            // when true, auto-scroll to bottom on log updates
	allocSelectMode      bool            // when true, ↑/↓ navigates allocations instead of scrolling in job-status view
	evalSelectMode       bool            // when true, ↑/↓ navigates evaluations instead of scrolling in job-status view
	selectedEvalIndex    int             // index of selected evaluation in job-status view
	selectedAlloc        *api.Allocation // currently selected allocation for alloc-detail view
	selectedEval         *api.Evaluation // currently selected evaluation for eval-detail view
	previousView         string          // track previous view for back navigation
	// Filter/search state
	filterActive     bool   // when true, show filter input overlay
	filterInput      string // current filter text input
	filteredJobs     []*jobStats
	filteredNodes    []*nodeStats
	filteredServices []*serviceInfo
	// Blocking query state
	jobsIndex      uint64 // LastIndex for jobs blocking query
	nodesIndex     uint64 // LastIndex for nodes blocking query
	servicesIndex  uint64 // LastIndex for services blocking query
	blockingActive bool   // true when a blocking query goroutine is running
}

type jobEvent struct {
	Time    time.Time
	AllocID string
	Task    string
	Type    string
	Message string
}

// --- Multi-selection helper functions ---

// ensureSelectedJobMap initializes the selection map if nil
func (m *model) ensureSelectedJobMap() {
	if m.selectedJobIDs == nil {
		m.selectedJobIDs = make(map[string]struct{})
	}
}

// selectSingleJob selects only the job at the given filtered index, clearing other selections
func (m *model) selectSingleJob(idx int) {
	m.ensureSelectedJobMap()
	m.selectedJobIDs = make(map[string]struct{})
	m.selectionAnchor = -1

	if idx < 0 || idx >= len(m.filteredJobs) {
		return
	}

	job := m.filteredJobs[idx]
	if job != nil {
		m.selectedJobIDs[job.ID] = struct{}{}
		m.selectionAnchor = idx
	}
}

// selectRangeJobs selects all jobs in a range between anchor and current (inclusive)
func (m *model) selectRangeJobs(anchor, current int) {
	m.ensureSelectedJobMap()

	if len(m.filteredJobs) == 0 {
		return
	}

	// Clamp indices to valid range
	if anchor < 0 {
		anchor = 0
	}
	if anchor >= len(m.filteredJobs) {
		anchor = len(m.filteredJobs) - 1
	}
	if current < 0 {
		current = 0
	}
	if current >= len(m.filteredJobs) {
		current = len(m.filteredJobs) - 1
	}

	// Ensure start <= end
	start, end := anchor, current
	if start > end {
		start, end = end, start
	}

	// Clear and rebuild selection
	m.selectedJobIDs = make(map[string]struct{})
	for i := start; i <= end; i++ {
		job := m.filteredJobs[i]
		if job != nil {
			m.selectedJobIDs[job.ID] = struct{}{}
		}
	}
	m.selectionAnchor = anchor
}

// pruneSelectedJobs removes jobs from selection that are no longer in the filtered list
func (m *model) pruneSelectedJobs() {
	m.ensureSelectedJobMap()

	if len(m.selectedJobIDs) == 0 {
		return
	}

	// Build set of valid job IDs from current filtered list
	valid := make(map[string]struct{})
	for _, job := range m.filteredJobs {
		if job == nil {
			continue
		}
		if _, selected := m.selectedJobIDs[job.ID]; selected {
			valid[job.ID] = struct{}{}
		}
	}

	m.selectedJobIDs = valid
	if len(valid) == 0 {
		m.selectionAnchor = -1
	}
}

// gatherSelectedJobs returns all currently selected jobs, or falls back to cursor position
func (m *model) gatherSelectedJobs() []*jobStats {
	m.ensureSelectedJobMap()

	// If we have multi-selections, return them
	if len(m.selectedJobIDs) > 0 {
		jobs := make([]*jobStats, 0, len(m.selectedJobIDs))
		for _, job := range m.filteredJobs {
			if job == nil {
				continue
			}
			if _, selected := m.selectedJobIDs[job.ID]; selected {
				jobs = append(jobs, job)
			}
		}
		if len(jobs) > 0 {
			return jobs
		}
	}

	// Fallback: return job at current cursor position
	if m.selectedIndex >= 0 && m.selectedIndex < len(m.filteredJobs) {
		job := m.filteredJobs[m.selectedIndex]
		if job != nil {
			return []*jobStats{job}
		}
	}

	return nil
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
	// Blocking query indices
	jobsIndex     uint64
	nodesIndex    uint64
	servicesIndex uint64
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
	jobs, jobsMeta, err := client.Jobs().List(nil)
	if err != nil {
		return errMsg(err)
	}
	nodes, nodesMeta, err := client.Nodes().List(nil)
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
	var servicesIndex uint64
	serviceStubs, servicesMeta, err := client.Services().List(nil)
	if err == nil {
		if servicesMeta != nil {
			servicesIndex = servicesMeta.LastIndex
		}
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

	return dataMsg{jobs: jobStatsList, nodes: nodeStatsList, services: servicesList, totalAvailCPU: totalAvailCPU, totalAvailMem: totalAvailMem, totalCapacityCPU: totalCapacityCPU, totalCapacityMem: totalCapacityMem, totalReservedCPU: totalReservedCPU, totalUsedCPU: totalUsedCPU, totalReservedMem: totalReservedMem, totalUsedMem: totalUsedMem, jobsIndex: jobsMeta.LastIndex, nodesIndex: nodesMeta.LastIndex, servicesIndex: servicesIndex}
}

func stopJob(client *api.Client, jobID string) tea.Msg {
	_, _, err := client.Jobs().Deregister(jobID, false, nil)
	if err != nil {
		return errMsg(err)
	}
	return refreshMsg{}
}

// blockingQueryMsg is sent when a blocking query detects a change or times out
type blockingQueryMsg struct {
	changed bool
	err     error
}

// startBlockingQuery starts a blocking query that monitors for changes to jobs, nodes, or services.
// It uses the Nomad blocking query mechanism: if WaitIndex is set, the API will block until
// data changes (new LastIndex > WaitIndex) or timeout (default 5 minutes).
// Returns a tea.Cmd that spawns a goroutine to perform the blocking query.
func startBlockingQuery(client *api.Client, jobsIndex, nodesIndex, servicesIndex uint64) tea.Cmd {
	return func() tea.Msg {
		// We use a short wait time to be responsive, but still benefit from blocking
		// Default Nomad wait is 5 minutes, but we use 30 seconds for better UX
		waitTime := 30 * time.Second

		// Try blocking on jobs first (most common changes)
		jobsOpts := &api.QueryOptions{
			WaitIndex: jobsIndex,
			WaitTime:  waitTime,
		}
		_, jobsMeta, err := client.Jobs().List(jobsOpts)
		if err != nil {
			// On error, signal to retry with a fresh fetch
			return blockingQueryMsg{changed: false, err: err}
		}

		// If jobs changed, trigger a full refresh
		if jobsMeta.LastIndex > jobsIndex {
			return blockingQueryMsg{changed: true, err: nil}
		}

		// Check nodes (blocking query already returned, so just check current state)
		nodesOpts := &api.QueryOptions{
			WaitIndex: nodesIndex,
			WaitTime:  1 * time.Second, // Short wait since we already blocked on jobs
		}
		_, nodesMeta, err := client.Nodes().List(nodesOpts)
		if err != nil {
			return blockingQueryMsg{changed: false, err: err}
		}
		if nodesMeta.LastIndex > nodesIndex {
			return blockingQueryMsg{changed: true, err: nil}
		}

		// Check services
		servicesOpts := &api.QueryOptions{
			WaitIndex: servicesIndex,
			WaitTime:  1 * time.Second,
		}
		_, servicesMeta, err := client.Services().List(servicesOpts)
		if err != nil {
			// Services might not be available, don't treat as fatal
			return blockingQueryMsg{changed: false, err: nil}
		}
		if servicesMeta != nil && servicesMeta.LastIndex > servicesIndex {
			return blockingQueryMsg{changed: true, err: nil}
		}

		// No changes detected during the blocking period
		return blockingQueryMsg{changed: false, err: nil}
	}
}

func deleteJob(client *api.Client, jobID string, namespace string) tea.Msg {
	var opts *api.WriteOptions
	if namespace != "" {
		opts = &api.WriteOptions{Namespace: namespace}
	}
	_, _, err := client.Jobs().Deregister(jobID, true, opts)
	if err != nil {
		return errMsg(err)
	}
	return refreshMsg{}
}

// deleteJobs deletes multiple jobs, deduplicating by job ID
func deleteJobs(client *api.Client, jobs []*jobStats) tea.Msg {
	seen := make(map[string]struct{})
	for _, job := range jobs {
		if job == nil {
			continue
		}
		// Skip duplicates
		if _, exists := seen[job.ID]; exists {
			continue
		}
		seen[job.ID] = struct{}{}

		// Delete the job with its namespace
		if msg := deleteJob(client, job.ID, job.Namespace); msg != nil {
			if _, ok := msg.(errMsg); ok {
				// Return first error encountered
				return msg
			}
		}
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

func fetchAllocEvents(client *api.Client, alloc *api.Allocation) tea.Cmd {
	return func() tea.Msg {
		var events []jobEvent

		allocIDShort := alloc.ID
		if len(allocIDShort) > 8 {
			allocIDShort = allocIDShort[:8]
		}

		// Iterate through all task states in this allocation
		for taskName, taskState := range alloc.TaskStates {
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

		// Sort events by time (most recent first)
		sort.Slice(events, func(i, j int) bool {
			return events[i].Time.After(events[j].Time)
		})

		if len(events) == 0 {
			return eventsMsg{err: fmt.Errorf("no events found for allocation %s", allocIDShort)}
		}

		return eventsMsg{
			events:  events,
			jobName: alloc.JobID,
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

	// Ensure availableWidth is at least 2 per column
	// (we subtract 1 for padding, so each column needs minimum 2)
	minRequired := numCols * 2
	if availableWidth < minRequired {
		availableWidth = minRequired
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

	// If available width is less than minimum, scale down proportionally
	if availableWidth <= totalMinWidth {
		// Scale down the minimum widths proportionally
		for i := range widths {
			widths[i] = (availableWidth * minWidths[i]) / totalMinWidth
			// Ensure at least 2 characters per column (we subtract 1 for padding)
			if widths[i] < 2 {
				widths[i] = 2
			}
		}
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

	// Final safety check: ensure minimum width of 2
	// (we often subtract 1 for padding, so width must be at least 2)
	for i := range widths {
		if widths[i] < 2 {
			widths[i] = 2
		}
	}

	return widths
}

// getTableWidth returns the available width for table content
// Accounts for: 2 chars left margin + border chars between/around columns
func getTableWidth(termWidth int, numColumns int) int {
	// 2 for left margin "  ", 1 for each column border (numColumns + 1 total)
	overhead := 2 + numColumns + 1
	available := termWidth - overhead
	minWidth := numColumns * 4 // minimum 4 chars per column
	if available < minWidth {
		return minWidth
	}
	// Extra safety: ensure we never return a value less than numColumns * 2
	// (each column needs at least 2 chars because we subtract 1 for padding)
	minSafeWidth := numColumns * 2
	if available < minSafeWidth {
		return minSafeWidth
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

// safeRepeat safely repeats a string, ensuring count is never negative
func safeRepeat(s string, count int) string {
	if count < 0 {
		return ""
	}
	return strings.Repeat(s, count)
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

// stripAnsi removes ANSI escape codes from a string for length calculation
func stripAnsi(s string) string {
	// Remove ANSI escape sequences
	ansiPattern := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	return ansiPattern.ReplaceAllString(s, "")
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
	topBorder := "  " + bold + headerColor + "╔" + safeRepeat("═", boxWidth) + "╗" + reset + "\n"

	// Title with padding (account for 2 spaces before title)
	// Use displayWidth for proper emoji handling
	titleDisplayWidth := displayWidth(title) + 2 // "  " + title
	padding := boxWidth - titleDisplayWidth
	if padding < 0 {
		padding = 0
	}
	middleLine := "  " + bold + headerColor + "║" + reset + "  " + bold + title + reset + safeRepeat(" ", padding) + bold + headerColor + "║" + reset + "\n"

	bottomBorder := "  " + bold + headerColor + "╚" + safeRepeat("═", boxWidth) + "╝" + reset + "\n"

	return topBorder + middleLine + bottomBorder
}

type errMsg error

type refreshMsg struct{}

type logsRefreshMsg struct{}

func (m model) Init() tea.Cmd {
	// Initial load: fetch data immediately, then start with a heartbeat tick
	// Blocking queries will be started after first data load
	return tea.Batch(
		tea.ClearScreen,
		tea.Cmd(func() tea.Msg { return fetchData(m.client) }),
		tea.Tick(30*time.Second, func(t time.Time) tea.Msg { return tickMsg{} }),
	)
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
				} else if len(m.confirmJobs) > 0 {
					// Job actions (single or multi)
					if m.confirmAction == "stop" {
						// Stop only works for single job
						jobID := m.confirmJobs[0].ID
						cmd = tea.Cmd(func() tea.Msg { return stopJob(m.client, jobID) })
					} else if m.confirmAction == "delete" {
						// Delete can handle multiple jobs
						jobs := m.confirmJobs
						cmd = tea.Cmd(func() tea.Msg { return deleteJobs(m.client, jobs) })
					}
					m.confirmJobs = nil
				}
				m.confirmAction = ""
				return m, cmd
			case "n", "N", "esc":
				m.confirmAction = ""
				m.confirmJob = nil
				m.confirmJobs = nil
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
			}
			return m, nil
		}
		// Handle filter input
		if m.filterActive {
			switch key {
			case "esc":
				m.filterActive = false
				m.filterInput = ""
				// Rebuild filtered lists
				m.filteredJobs = m.buildFilteredJobs()
				m.filteredNodes = m.buildFilteredNodes()
				m.filteredServices = m.buildFilteredServices()
				// Reset selection to 0 when clearing filter
				if m.view == "jobs" {
					m.selectedIndex = 0
					m.jobsScrollOffset = 0
				} else if m.view == "nodes" {
					m.selectedNodeIndex = 0
					m.nodesScrollOffset = 0
				} else if m.view == "services" {
					m.selectedServiceIndex = 0
					m.servicesScrollOffset = 0
				}
				return m, nil
			case "enter":
				m.filterActive = false
				return m, nil
			case "backspace":
				if len(m.filterInput) > 0 {
					m.filterInput = m.filterInput[:len(m.filterInput)-1]
					// Rebuild filtered lists
					m.filteredJobs = m.buildFilteredJobs()
					m.filteredNodes = m.buildFilteredNodes()
					m.filteredServices = m.buildFilteredServices()
					// Reset selection if it's out of bounds
					if m.view == "jobs" && m.selectedIndex >= len(m.filteredJobs) {
						m.selectedIndex = 0
						m.jobsScrollOffset = 0
					} else if m.view == "nodes" && m.selectedNodeIndex >= len(m.filteredNodes) {
						m.selectedNodeIndex = 0
						m.nodesScrollOffset = 0
					} else if m.view == "services" && m.selectedServiceIndex >= len(m.filteredServices) {
						m.selectedServiceIndex = 0
						m.servicesScrollOffset = 0
					}
				}
				return m, nil
			default:
				// Add printable characters to filter input
				if len(key) == 1 && key >= " " && key <= "~" {
					m.filterInput += key
					// Rebuild filtered lists
					m.filteredJobs = m.buildFilteredJobs()
					m.filteredNodes = m.buildFilteredNodes()
					m.filteredServices = m.buildFilteredServices()
					// Reset selection if it's out of bounds
					if m.view == "jobs" && m.selectedIndex >= len(m.filteredJobs) {
						m.selectedIndex = 0
						m.jobsScrollOffset = 0
					} else if m.view == "nodes" && m.selectedNodeIndex >= len(m.filteredNodes) {
						m.selectedNodeIndex = 0
						m.nodesScrollOffset = 0
					} else if m.view == "services" && m.selectedServiceIndex >= len(m.filteredServices) {
						m.selectedServiceIndex = 0
						m.servicesScrollOffset = 0
					}
				}
				return m, nil
			}
		}
		switch key {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "r":
			if m.view == "job-logs" && m.selectedJobIndex >= 0 && m.selectedJobIndex < len(m.jobs) {
				selectedJob := m.jobs[m.selectedJobIndex]
				m.logContent = "Loading logs..."
				// Use the selected allocation if one is selected and valid
				if m.selectedAllocIndex >= 0 && m.selectedAllocIndex < len(selectedJob.allocs) {
					selectedAlloc := selectedJob.allocs[m.selectedAllocIndex]
					return m, fetchAllocLogs(m.client, selectedAlloc, selectedJob.Name)
				}
				// Fall back to finding any running allocation
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
					m.selectedEvalIndex = 0
					m.scrollOffset = 0
					m.allocSelectMode = false
					m.evalSelectMode = false
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
			if m.view == "job-logs" {
				// Toggle pause/follow mode in logs view
				m.logsFollowMode = !m.logsFollowMode
				if m.logsFollowMode {
					// When enabling follow mode, jump to bottom
					m.logsScrollOffset = 999999
				}
			} else if m.view == "job-status" {
				// Previous job
				if m.selectedJobIndex > 0 {
					m.selectedJobIndex--
					m.selectedAllocIndex = 0
					m.selectedEvalIndex = 0
					m.scrollOffset = 0
					m.allocSelectMode = false
					m.evalSelectMode = false
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
				// Return to previous view (job-status or alloc-detail)
				if m.previousView != "" {
					m.view = m.previousView
					m.previousView = ""
				} else {
					m.view = "job-status"
				}
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
					m.filterActive = false
					m.filterInput = ""
				} else if m.evalSelectMode {
					// Exit evaluation selection mode first
					m.evalSelectMode = false
					m.filterActive = false
					m.filterInput = ""
				} else {
					m.view = "jobs"
				}
			}
			if m.view == "job-logs" {
				// Return to previous view (job-status or alloc-detail)
				if m.previousView != "" {
					m.view = m.previousView
					m.previousView = ""
				} else {
					m.view = "job-status"
				}
			}
			if m.view == "job-events" {
				m.view = "job-status"
			}
			if m.view == "alloc-detail" {
				m.view = "job-status"
				m.allocSelectMode = false
			}
			if m.view == "alloc-events" {
				// Return to previous view (should be alloc-detail)
				if m.previousView != "" {
					m.view = m.previousView
					m.previousView = ""
				} else {
					m.view = "alloc-detail"
				}
			}
			if m.view == "eval-detail" {
				m.view = "job-status"
				m.evalSelectMode = false
			}
		case "v":
			m.view = "services"
		case "c":
			m.view = "cluster"
			m.scrollOffset = 0
		case "shift+up":
			// Multi-select: extend selection upward
			if m.view == "jobs" && m.selectedIndex > 0 {
				if m.selectionAnchor < 0 {
					m.selectionAnchor = m.selectedIndex
				}
				m.selectedIndex--
				m.selectRangeJobs(m.selectionAnchor, m.selectedIndex)
				if m.selectedIndex < m.jobsScrollOffset {
					m.jobsScrollOffset = m.selectedIndex
				}
			}
		case "up":
			if m.view == "jobs" && m.selectedIndex > 0 {
				m.selectedIndex--
				m.selectSingleJob(m.selectedIndex)
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
				} else if m.evalSelectMode {
					// Navigate evaluations
					if m.selectedEvalIndex > 0 {
						m.selectedEvalIndex--
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
			if m.view == "job-logs" && m.logsScrollOffset > 0 {
				m.logsScrollOffset--
				// Disable follow mode when manually scrolling
				m.logsFollowMode = false
			}
		case "shift+down":
			// Multi-select: extend selection downward
			if m.view == "jobs" {
				maxJobs := len(m.filteredJobs)
				if maxJobs == 0 {
					maxJobs = len(m.jobs)
				}
				if m.selectedIndex < maxJobs-1 {
					if m.selectionAnchor < 0 {
						m.selectionAnchor = m.selectedIndex
					}
					m.selectedIndex++
					m.selectRangeJobs(m.selectionAnchor, m.selectedIndex)
					if m.height > 0 {
						chromeLines := 15
						maxVisibleJobs := m.height - chromeLines
						if maxVisibleJobs < 1 {
							maxVisibleJobs = 1
						}
						scrollBuffer := 2
						if m.selectedIndex >= m.jobsScrollOffset+maxVisibleJobs-scrollBuffer {
							m.jobsScrollOffset = m.selectedIndex - maxVisibleJobs + scrollBuffer + 1
						}
					}
				}
			}
		case "down":
			if m.view == "jobs" {
				maxJobs := len(m.filteredJobs)
				if maxJobs == 0 {
					maxJobs = len(m.jobs)
				}
				if m.selectedIndex < maxJobs-1 {
					m.selectedIndex++
					m.selectSingleJob(m.selectedIndex)
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
			}
			if m.view == "nodes" {
				maxNodes := len(m.filteredNodes)
				if maxNodes == 0 {
					maxNodes = len(m.nodes)
				}
				if m.selectedNodeIndex < maxNodes-1 {
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
			}
			if m.view == "services" {
				maxServices := len(m.filteredServices)
				if maxServices == 0 {
					maxServices = len(m.services)
				}
				if m.selectedServiceIndex < maxServices-1 {
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
				} else if m.evalSelectMode {
					// Navigate evaluations
					if m.selectedJobIndex >= 0 && m.selectedJobIndex < len(m.jobs) {
						selectedJob := m.jobs[m.selectedJobIndex]
						if m.selectedEvalIndex < len(selectedJob.evaluations)-1 {
							m.selectedEvalIndex++
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
			if m.view == "job-logs" {
				m.logsScrollOffset++
				// Disable follow mode when manually scrolling
				m.logsFollowMode = false
			}
		case "s":
			if m.view == "jobs" && len(m.filteredJobs) > 0 && m.confirmAction == "" {
				jobs := m.gatherSelectedJobs()
				if len(jobs) == 1 {
					m.confirmAction = "stop"
					m.confirmJobs = jobs
				}
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
			if m.view == "jobs" && len(m.filteredJobs) > 0 && m.confirmAction == "" {
				jobs := m.gatherSelectedJobs()
				if len(jobs) > 0 {
					m.confirmAction = "delete"
					m.confirmJobs = jobs
				}
			}
		case "i":
			if m.view == "jobs" {
				originalIndex := m.getOriginalJobIndex(m.selectedIndex)
				m.view = "job-status"
				m.selectedJobIndex = originalIndex
				m.selectedAllocIndex = 0
				m.scrollOffset = 0
			}
		case "enter":
			if m.view == "jobs" {
				originalIndex := m.getOriginalJobIndex(m.selectedIndex)
				m.view = "job-status"
				m.selectedJobIndex = originalIndex
				m.selectedAllocIndex = 0
				m.selectedEvalIndex = 0
				m.scrollOffset = 0
			}
			if m.view == "nodes" {
				m.selectedNodeIndex = m.getOriginalNodeIndex(m.selectedNodeIndex)
				m.view = "node-status"
				m.scrollOffset = 0
			}
			// Enter allocation detail view when in allocation selection mode
			if m.view == "job-status" && m.allocSelectMode && m.selectedJobIndex >= 0 && m.selectedJobIndex < len(m.jobs) {
				selectedJob := m.jobs[m.selectedJobIndex]
				if m.selectedAllocIndex >= 0 && m.selectedAllocIndex < len(selectedJob.allocs) {
					allocStub := selectedJob.allocs[m.selectedAllocIndex]
					// Get full allocation from allocMap
					if fullAlloc, ok := selectedJob.allocMap[allocStub.ID]; ok {
						m.selectedAlloc = fullAlloc
						m.view = "alloc-detail"
						m.scrollOffset = 0
					}
				}
			}
			// Enter evaluation detail view when in evaluation selection mode
			if m.view == "job-status" && m.evalSelectMode && m.selectedJobIndex >= 0 && m.selectedJobIndex < len(m.jobs) {
				selectedJob := m.jobs[m.selectedJobIndex]
				if m.selectedEvalIndex >= 0 && m.selectedEvalIndex < len(selectedJob.evaluations) {
					m.selectedEval = selectedJob.evaluations[m.selectedEvalIndex]
					m.view = "eval-detail"
					m.scrollOffset = 0
				}
			}
		case "l":
			if m.view == "job-status" && len(m.jobs) > 0 && m.selectedJobIndex >= 0 && m.selectedJobIndex < len(m.jobs) {
				selectedJob := m.jobs[m.selectedJobIndex]
				m.logContent = "Loading logs..."
				m.logJobName = selectedJob.Name
				m.previousView = m.view
				m.view = "job-logs"
				m.logsScrollOffset = 0  // Reset scroll when opening logs
				m.logsFollowMode = true // Enable follow mode by default
				// Use the selected allocation if one is selected and valid
				if m.selectedAllocIndex >= 0 && m.selectedAllocIndex < len(selectedJob.allocs) {
					selectedAlloc := selectedJob.allocs[m.selectedAllocIndex]
					return m, fetchAllocLogs(m.client, selectedAlloc, selectedJob.Name)
				}
				// Fall back to finding any running allocation
				return m, fetchLogs(m.client, selectedJob)
			}
			// In alloc-detail view: show allocation logs
			if m.view == "alloc-detail" && m.selectedAlloc != nil {
				m.logContent = "Loading logs..."
				m.logJobName = m.selectedAlloc.JobID
				m.previousView = m.view
				m.view = "job-logs"
				m.logsScrollOffset = 0  // Reset scroll when opening logs
				m.logsFollowMode = true // Enable follow mode by default
				// Convert Allocation to AllocationListStub for fetchAllocLogs
				allocStub := &api.AllocationListStub{
					ID:           m.selectedAlloc.ID,
					JobID:        m.selectedAlloc.JobID,
					TaskGroup:    m.selectedAlloc.TaskGroup,
					ClientStatus: m.selectedAlloc.ClientStatus,
				}
				return m, fetchAllocLogs(m.client, allocStub, m.selectedAlloc.JobID)
			}
		case "e":
			// In job-status view: enter evaluation selection mode
			if m.view == "job-status" && len(m.jobs) > 0 && m.selectedJobIndex >= 0 && m.selectedJobIndex < len(m.jobs) {
				selectedJob := m.jobs[m.selectedJobIndex]
				if len(selectedJob.evaluations) > 0 {
					m.evalSelectMode = !m.evalSelectMode
					m.allocSelectMode = false // Exit alloc mode if in it
					// Clear filter when entering or exiting eval select mode
					m.filterActive = false
					m.filterInput = ""
					if m.evalSelectMode {
						// Ensure valid selection
						if m.selectedEvalIndex < 0 || m.selectedEvalIndex >= len(selectedJob.evaluations) {
							m.selectedEvalIndex = 0
						}
					}
				}
			}
			// In alloc-detail view: show allocation events
			if m.view == "alloc-detail" && m.selectedAlloc != nil {
				m.eventsList = nil
				m.eventsJobName = m.selectedAlloc.JobID
				m.previousView = m.view
				m.view = "alloc-events"
				return m, fetchAllocEvents(m.client, m.selectedAlloc)
			}
		case "a":
			// Toggle allocation selection mode in job-status view
			if m.view == "job-status" && m.selectedJobIndex >= 0 && m.selectedJobIndex < len(m.jobs) {
				selectedJob := m.jobs[m.selectedJobIndex]
				if len(selectedJob.allocs) > 0 {
					m.allocSelectMode = !m.allocSelectMode
					m.evalSelectMode = false // Exit eval mode if in it
					// Clear filter when entering or exiting alloc select mode
					m.filterActive = false
					m.filterInput = ""
					if m.allocSelectMode {
						// Ensure valid selection
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
		case "/", "f":
			// Activate filter mode in applicable views
			if m.view == "jobs" || m.view == "nodes" || m.view == "services" || m.view == "job-status" {
				m.filterActive = true
				m.filterInput = ""
				// Initialize filtered lists
				m.filteredJobs = m.buildFilteredJobs()
				m.filteredNodes = m.buildFilteredNodes()
				m.filteredServices = m.buildFilteredServices()
			}
		case "home":
			// Jump to top of logs
			if m.view == "job-logs" {
				m.logsScrollOffset = 0
				// Disable follow mode when manually scrolling
				m.logsFollowMode = false
			}
		case "end":
			// Jump to bottom of logs
			if m.view == "job-logs" {
				// Set to max scroll (will be clamped in View)
				m.logsScrollOffset = 999999
			}
		case "pgup":
			// Page up in logs
			if m.view == "job-logs" {
				pageSize := m.height - 12
				if pageSize < 5 {
					pageSize = 5
				}
				m.logsScrollOffset -= pageSize
				if m.logsScrollOffset < 0 {
					m.logsScrollOffset = 0
				}
				// Disable follow mode when manually scrolling
				m.logsFollowMode = false
			}
		case "pgdn":
			// Page down in logs
			if m.view == "job-logs" {
				pageSize := m.height - 12
				if pageSize < 5 {
					pageSize = 5
				}
				m.logsScrollOffset += pageSize
			}
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
		m.totalReservedMem = msg.totalReservedMem
		m.totalUsedCPU = msg.totalUsedCPU
		m.totalUsedMem = msg.totalUsedMem

		// Rebuild filtered lists when data changes
		m.filteredJobs = m.buildFilteredJobs()
		m.filteredNodes = m.buildFilteredNodes()
		m.filteredServices = m.buildFilteredServices()

		// Prune stale job selections after data refresh
		m.pruneSelectedJobs()

		// Update blocking query indices
		m.jobsIndex = msg.jobsIndex
		m.nodesIndex = msg.nodesIndex
		m.servicesIndex = msg.servicesIndex

		// Update selected allocation if we're viewing alloc details
		if m.view == "alloc-detail" && m.selectedAlloc != nil {
			// Find the updated allocation data
			for _, job := range m.jobs {
				if fullAlloc, ok := job.allocMap[m.selectedAlloc.ID]; ok {
					m.selectedAlloc = fullAlloc
					break
				}
			}
		}

		// Update selected evaluation if we're viewing eval details
		if m.view == "eval-detail" && m.selectedEval != nil && m.selectedJobIndex >= 0 && m.selectedJobIndex < len(m.jobs) {
			// Find the updated evaluation data
			selectedJob := m.jobs[m.selectedJobIndex]
			for _, eval := range selectedJob.evaluations {
				if eval.ID == m.selectedEval.ID {
					m.selectedEval = eval
					break
				}
			}
		}

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
		// Start blocking query to wait for changes
		m.blockingActive = true
		return m, startBlockingQuery(m.client, m.jobsIndex, m.nodesIndex, m.servicesIndex)
	case blockingQueryMsg:
		m.blockingActive = false
		if msg.err != nil {
			// On error, schedule a retry after a short delay
			return m, tea.Tick(5*time.Second, func(t time.Time) tea.Msg { return tickMsg{} })
		}
		if msg.changed {
			// Data changed, fetch fresh data
			return m, tea.Cmd(func() tea.Msg { return fetchData(m.client) })
		}
		// No change detected, start another blocking query
		m.blockingActive = true
		return m, startBlockingQuery(m.client, m.jobsIndex, m.nodesIndex, m.servicesIndex)
	case tickMsg:
		// Heartbeat tick - if blocking query isn't active, start a fresh fetch
		if !m.blockingActive {
			return m, tea.Cmd(func() tea.Msg { return fetchData(m.client) })
		}
		// Blocking query is active, just reschedule the heartbeat
		return m, tea.Tick(30*time.Second, func(t time.Time) tea.Msg { return tickMsg{} })
	case errMsg:
		m.err = error(msg)
		m.blockingActive = false
		// On error, retry after a delay
		return m, tea.Tick(5*time.Second, func(t time.Time) tea.Msg { return tickMsg{} })
	case refreshMsg:
		return m, tea.Cmd(func() tea.Msg { return fetchData(m.client) })
	case logsMsg:
		if msg.err != nil {
			m.logContent = fmt.Sprintf("Error: %v", msg.err)
		} else {
			m.logContent = msg.content
			m.logAllocID = msg.allocID
			m.logTaskName = msg.taskName
			// Auto-scroll to bottom when new logs are loaded AND follow mode is enabled
			if m.logsFollowMode {
				m.logsScrollOffset = 999999 // Will be clamped to max in View
			}
		}
		// If in follow mode and still viewing logs, schedule next refresh
		if m.logsFollowMode && m.view == "job-logs" {
			return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg { return logsRefreshMsg{} })
		}
		return m, nil
	case logsRefreshMsg:
		// Auto-refresh logs when in follow mode
		if m.logsFollowMode && m.view == "job-logs" && m.selectedJobIndex >= 0 && m.selectedJobIndex < len(m.jobs) {
			selectedJob := m.jobs[m.selectedJobIndex]
			// Use the selected allocation if one is selected and valid
			if m.selectedAllocIndex >= 0 && m.selectedAllocIndex < len(selectedJob.allocs) {
				selectedAlloc := selectedJob.allocs[m.selectedAllocIndex]
				return m, fetchAllocLogs(m.client, selectedAlloc, selectedJob.Name)
			}
			// Fall back to finding any running allocation
			return m, fetchLogs(m.client, selectedJob)
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

// matchesFilter returns true if the text contains the filter string (case-insensitive)
func matchesFilter(text string, filter string) bool {
	if filter == "" {
		return true
	}
	return strings.Contains(strings.ToLower(text), strings.ToLower(filter))
}

// buildFilteredJobs returns filtered job list based on current filter
func (m *model) buildFilteredJobs() []*jobStats {
	if m.filterInput == "" {
		return m.jobs
	}
	filtered := make([]*jobStats, 0)
	for _, job := range m.jobs {
		if matchesFilter(job.Name, m.filterInput) {
			filtered = append(filtered, job)
		}
	}
	return filtered
}

// buildFilteredNodes returns filtered node list based on current filter
func (m *model) buildFilteredNodes() []*nodeStats {
	if m.filterInput == "" {
		return m.nodes
	}
	filtered := make([]*nodeStats, 0)
	for _, node := range m.nodes {
		if matchesFilter(node.Name, m.filterInput) || matchesFilter(node.ID, m.filterInput) {
			filtered = append(filtered, node)
		}
	}
	return filtered
}

// buildFilteredServices returns filtered service list based on current filter
func (m *model) buildFilteredServices() []*serviceInfo {
	if m.filterInput == "" {
		return m.services
	}
	filtered := make([]*serviceInfo, 0)
	for _, svc := range m.services {
		if matchesFilter(svc.Name, m.filterInput) {
			filtered = append(filtered, svc)
		}
	}
	return filtered
}

// getOriginalJobIndex maps filtered index to original job list index
func (m *model) getOriginalJobIndex(filteredIndex int) int {
	if m.filterInput == "" || filteredIndex < 0 || filteredIndex >= len(m.filteredJobs) {
		return filteredIndex
	}
	targetJob := m.filteredJobs[filteredIndex]
	for i, job := range m.jobs {
		if job == targetJob {
			return i
		}
	}
	return filteredIndex
}

// getOriginalNodeIndex maps filtered index to original node list index
func (m *model) getOriginalNodeIndex(filteredIndex int) int {
	if m.filterInput == "" || filteredIndex < 0 || filteredIndex >= len(m.filteredNodes) {
		return filteredIndex
	}
	targetNode := m.filteredNodes[filteredIndex]
	for i, node := range m.nodes {
		if node == targetNode {
			return i
		}
	}
	return filteredIndex
}

// getOriginalServiceIndex maps filtered index to original service list index
func (m *model) getOriginalServiceIndex(filteredIndex int) int {
	if m.filterInput == "" || filteredIndex < 0 || filteredIndex >= len(m.filteredServices) {
		return filteredIndex
	}
	targetService := m.filteredServices[filteredIndex]
	for i, svc := range m.services {
		if svc == targetService {
			return i
		}
	}
	return filteredIndex
}

// highlightMatch highlights the filter match in the text with color
func highlightMatch(text string, filter string, highlightColor string, reset string) string {
	if filter == "" {
		return text
	}
	lowerText := strings.ToLower(text)
	lowerFilter := strings.ToLower(filter)
	index := strings.Index(lowerText, lowerFilter)
	if index == -1 {
		return text
	}
	// Highlight the matching portion
	before := text[:index]
	match := text[index : index+len(filter)]
	after := text[index+len(filter):]
	return before + highlightColor + match + reset + after
}

// renderFilterOverlay renders a centered search/filter input box
func renderFilterOverlay(width int, height int, filterInput string, theme Theme) string {
	boxWidth := 60
	if boxWidth > width-4 {
		boxWidth = width - 4
	}

	// Calculate position (centered)
	startRow := (height - 5) / 2
	if startRow < 0 {
		startRow = 0
	}
	leftPad := (width - boxWidth) / 2
	if leftPad < 0 {
		leftPad = 0
	}

	padding := safeRepeat(" ", leftPad)
	cyan := "\033[36m"
	reset := "\033[0m"

	// Build the overlay
	var lines []string

	// Add blank lines before box
	for i := 0; i < startRow; i++ {
		lines = append(lines, "")
	}

	// Top border
	lines = append(lines, padding+"╭"+safeRepeat("─", boxWidth-2)+"╮")

	// Title line
	title := " Filter (Esc to cancel, Enter to apply) "
	titlePad := (boxWidth - 2 - len(title)) / 2
	if titlePad < 0 {
		titlePad = 0
	}
	lines = append(lines, padding+"│"+safeRepeat(" ", titlePad)+cyan+title+reset+safeRepeat(" ", boxWidth-2-titlePad-len(title))+"│")

	// Separator
	lines = append(lines, padding+"├"+safeRepeat("─", boxWidth-2)+"┤")

	// Input line with cursor
	inputDisplay := filterInput + "█" // cursor
	inputPadding := boxWidth - 4 - len(inputDisplay)
	if inputPadding < 0 {
		inputPadding = 0
		inputDisplay = inputDisplay[:boxWidth-4]
	}
	lines = append(lines, padding+"│ "+inputDisplay+safeRepeat(" ", inputPadding)+" │")

	// Bottom border
	lines = append(lines, padding+"╰"+safeRepeat("─", boxWidth-2)+"╯")

	return strings.Join(lines, "\n")
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
			return line + safeRepeat(" ", padding)
		}

		// === ROW 1: NAVIGATION header / JOB ACTIONS header ===
		leftRow := "  " + bold + cyan + "NAVIGATION" + reset
		rightRow := bold + cyan + "JOB ACTIONS" + reset + "  " + dimmed + "(jobs view)" + reset
		helpLines = append(helpLines, padLeft(leftRow, 12)+gap+rightRow)

		// === ROW 2: NAVIGATION top border / JOB ACTIONS top border ===
		leftRow = "  " + dimmed + "╭" + safeRepeat("─", colKey) + "┬" + safeRepeat("─", colDesc) + "╮" + reset
		rightRow = dimmed + "╭" + safeRepeat("─", colKey) + "┬" + safeRepeat("─", colDesc) + "╮" + reset
		helpLines = append(helpLines, padLeft(leftRow, leftVisualWidth)+gap+rightRow)

		// === ROW 3: Column headers ===
		leftRow = "  " + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colKey-1, "Key") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colDesc-1, "Action") + reset + dimmed + "│" + reset
		rightRow = dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colKey-1, "Key") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colDesc-1, "Action") + reset + dimmed + "│" + reset
		helpLines = append(helpLines, padLeft(leftRow, leftVisualWidth)+gap+rightRow)

		// === ROW 4: Separators ===
		leftRow = "  " + dimmed + "├" + safeRepeat("─", colKey) + "┼" + safeRepeat("─", colDesc) + "┤" + reset
		rightRow = dimmed + "├" + safeRepeat("─", colKey) + "┼" + safeRepeat("─", colDesc) + "┤" + reset
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
		rightRow = dimmed + "╰" + safeRepeat("─", colKey) + "┴" + safeRepeat("─", colDesc) + "╯" + reset
		helpLines = append(helpLines, padLeft(leftRow, leftVisualWidth)+gap+rightRow)

		// === ROW 11: bottom of NAVIGATION / empty ===
		leftRow = "  " + dimmed + "╰" + safeRepeat("─", colKey) + "┴" + safeRepeat("─", colDesc) + "╯" + reset
		helpLines = append(helpLines, padLeft(leftRow, leftVisualWidth))

		// === ROW 12: empty ===
		helpLines = append(helpLines, "")

		// === ROW 13: GENERAL header / NODE ACTIONS header ===
		leftRow = "  " + bold + cyan + "GENERAL" + reset
		rightRow = bold + cyan + "NODE ACTIONS" + reset + "  " + dimmed + "(nodes view)" + reset
		helpLines = append(helpLines, padLeft(leftRow, 9)+gap+rightRow)

		// === ROW 14: GENERAL top border / NODE ACTIONS top border ===
		leftRow = "  " + dimmed + "╭" + safeRepeat("─", colKey) + "┬" + safeRepeat("─", colDesc) + "╮" + reset
		rightRow = dimmed + "╭" + safeRepeat("─", colKey) + "┬" + safeRepeat("─", colDesc) + "╮" + reset
		helpLines = append(helpLines, padLeft(leftRow, leftVisualWidth)+gap+rightRow)

		// === ROW 15: Column headers ===
		leftRow = "  " + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colKey-1, "Key") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colDesc-1, "Action") + reset + dimmed + "│" + reset
		rightRow = dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colKey-1, "Key") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colDesc-1, "Action") + reset + dimmed + "│" + reset
		helpLines = append(helpLines, padLeft(leftRow, leftVisualWidth)+gap+rightRow)

		// === ROW 16: Separators ===
		leftRow = "  " + dimmed + "├" + safeRepeat("─", colKey) + "┼" + safeRepeat("─", colDesc) + "┤" + reset
		rightRow = dimmed + "├" + safeRepeat("─", colKey) + "┼" + safeRepeat("─", colDesc) + "┤" + reset
		helpLines = append(helpLines, padLeft(leftRow, leftVisualWidth)+gap+rightRow)

		// === ROW 17: r / Enter ===
		leftRow = "  " + dimmed + "│" + reset + " " + keyColor + fmt.Sprintf("%-*s", colKey-1, "r") + reset + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colDesc-1, "Refresh data") + dimmed + "│" + reset
		rightRow = dimmed + "│" + reset + " " + keyColor + fmt.Sprintf("%-*s", colKey-1, "Enter") + reset + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colDesc-1, "View node details") + dimmed + "│" + reset
		helpLines = append(helpLines, padLeft(leftRow, leftVisualWidth)+gap+rightRow)

		// === ROW 18: b / bottom of NODE ACTIONS ===
		leftRow = "  " + dimmed + "│" + reset + " " + keyColor + fmt.Sprintf("%-*s", colKey-1, "b") + reset + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colDesc-1, "Go back to previous view") + dimmed + "│" + reset
		rightRow = dimmed + "╰" + safeRepeat("─", colKey) + "┴" + safeRepeat("─", colDesc) + "╯" + reset
		helpLines = append(helpLines, padLeft(leftRow, leftVisualWidth)+gap+rightRow)

		// === ROW 19: h / empty ===
		leftRow = "  " + dimmed + "│" + reset + " " + keyColor + fmt.Sprintf("%-*s", colKey-1, "h") + reset + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colDesc-1, "Toggle this help screen") + dimmed + "│" + reset
		helpLines = append(helpLines, padLeft(leftRow, leftVisualWidth))

		// === ROW 20: q / empty ===
		leftRow = "  " + dimmed + "│" + reset + " " + keyColor + fmt.Sprintf("%-*s", colKey-1, "q") + reset + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colDesc-1, "Quit application") + dimmed + "│" + reset
		helpLines = append(helpLines, padLeft(leftRow, leftVisualWidth))

		// === ROW 21: bottom of GENERAL / empty ===
		leftRow = "  " + dimmed + "╰" + safeRepeat("─", colKey) + "┴" + safeRepeat("─", colDesc) + "╯" + reset
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
		helpLines = append(helpLines, leftRow+safeRepeat(" ", statusColorsWidth-15+extraPad)+gap+rightRow)

		// === ROW 24: STATUS COLORS top border / SERVICE ACTIONS top border ===
		leftRow = "  " + dimmed + "╭" + safeRepeat("─", colColor) + "┬" + safeRepeat("─", colMeaning) + "╮" + reset
		rightRow = dimmed + "╭" + safeRepeat("─", colKey) + "┬" + safeRepeat("─", colDesc) + "╮" + reset
		helpLines = append(helpLines, leftRow+safeRepeat(" ", extraPad)+gap+rightRow)

		// === ROW 25: Column headers ===
		leftRow = "  " + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colColor-1, "Color") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colMeaning-1, "Meaning") + reset + dimmed + "│" + reset
		rightRow = dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colKey-1, "Key") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colDesc-1, "Action") + reset + dimmed + "│" + reset
		helpLines = append(helpLines, leftRow+safeRepeat(" ", extraPad)+gap+rightRow)

		// === ROW 26: Separators ===
		leftRow = "  " + dimmed + "├" + safeRepeat("─", colColor) + "┼" + safeRepeat("─", colMeaning) + "┤" + reset
		rightRow = dimmed + "├" + safeRepeat("─", colKey) + "┼" + safeRepeat("─", colDesc) + "┤" + reset
		helpLines = append(helpLines, leftRow+safeRepeat(" ", extraPad)+gap+rightRow)

		// === ROW 27: Green / ↑ ↓ ===
		leftRow = "  " + dimmed + "│" + reset + " " + green + "●" + reset + fmt.Sprintf(" %-*s", colColor-3, "Green") + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colMeaning-1, "Running / Ready") + dimmed + "│" + reset
		rightRow = dimmed + "│" + reset + " " + keyColor + fmt.Sprintf("%-*s", colKey-1, "↑ ↓") + reset + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colDesc-1, "Navigate services") + dimmed + "│" + reset
		helpLines = append(helpLines, leftRow+safeRepeat(" ", extraPad)+gap+rightRow)

		// === ROW 28: Yellow / bottom of SERVICE ACTIONS ===
		leftRow = "  " + dimmed + "│" + reset + " " + yellow + "●" + reset + fmt.Sprintf(" %-*s", colColor-3, "Yellow") + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colMeaning-1, "Pending") + dimmed + "│" + reset
		rightRow = dimmed + "╰" + safeRepeat("─", colKey) + "┴" + safeRepeat("─", colDesc) + "╯" + reset
		helpLines = append(helpLines, leftRow+safeRepeat(" ", extraPad)+gap+rightRow)

		// === ROW 29: Red / empty ===
		leftRow = "  " + dimmed + "│" + reset + " " + red + "●" + reset + fmt.Sprintf(" %-*s", colColor-3, "Red") + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colMeaning-1, "Dead / Failed") + dimmed + "│" + reset
		helpLines = append(helpLines, leftRow)

		// === ROW 30: bottom of STATUS COLORS ===
		leftRow = "  " + dimmed + "╰" + safeRepeat("─", colColor) + "┴" + safeRepeat("─", colMeaning) + "╯" + reset
		helpLines = append(helpLines, leftRow)

		// === ROW 31: empty for spacing ===
		helpLines = append(helpLines, "")

		// === ROW 32: ALLOCATION ACTIONS header ===
		helpLines = append(helpLines, "  "+bold+cyan+"ALLOCATION ACTIONS"+reset+"  "+dimmed+"(job-status view, press 'a' to enter alloc mode)"+reset)

		// === ROW 33: ALLOCATION ACTIONS top border ===
		helpLines = append(helpLines, "  "+dimmed+"╭"+safeRepeat("─", colKey)+"┬"+safeRepeat("─", colDesc)+"╮"+reset)

		// === ROW 34: Column headers ===
		helpLines = append(helpLines, "  "+dimmed+"│"+reset+" "+bold+fmt.Sprintf("%-*s", colKey-1, "Key")+reset+dimmed+"│"+reset+" "+bold+fmt.Sprintf("%-*s", colDesc-1, "Action")+reset+dimmed+"│"+reset)

		// === ROW 35: Separator ===
		helpLines = append(helpLines, "  "+dimmed+"├"+safeRepeat("─", colKey)+"┼"+safeRepeat("─", colDesc)+"┤"+reset)

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
		helpLines = append(helpLines, "  "+dimmed+"╰"+safeRepeat("─", colKey)+"┴"+safeRepeat("─", colDesc)+"╯"+reset)

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
		footerBar := "\033[48;5;237m" + "\033[37m" + safeRepeat(" ", leftPad) + "Press '\033[38;5;51mh\033[37m' for help, '\033[38;5;204mq\033[37m' for quit" + safeRepeat(" ", rightPad) + "\033[0m"

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
		result += safeRepeat("─", m.width) + "\n"
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

		// Filter jobs if filter is active
		filteredJobs := m.jobs
		if m.filterInput != "" {
			filteredJobs = make([]*jobStats, 0)
			for _, job := range m.jobs {
				if matchesFilter(job.Name, m.filterInput) {
					filteredJobs = append(filteredJobs, job)
				}
			}
		}

		for _, job := range filteredJobs {
			switch job.Status {
			case "running":
				runningCount++
			case "pending":
				pendingCount++
			case "dead":
				deadCount++
			}
		}
		content += "  " + dimmed + "Total:" + reset + " " + bold + fmt.Sprintf("%d", len(filteredJobs)) + reset
		if m.filterInput != "" {
			content += " " + dimmed + "(filtered from " + fmt.Sprintf("%d", len(m.jobs)) + ")" + reset
		}
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
		content += "  " + dimmed + "╭" + safeRepeat("─", colName) + "┬" + safeRepeat("─", colStatus) + "┬" + safeRepeat("─", colType) + "┬" + safeRepeat("─", colPool) + "┬" + safeRepeat("─", colUptime) + "╮" + reset + "\n"
		content += "  " + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colName-1, "Job Name") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colStatus-1, "Status") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colType-1, "Type") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colPool-1, "Node Pool") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colUptime-1, "Uptime") + reset + dimmed + "│" + reset + "\n"
		content += "  " + dimmed + "├" + safeRepeat("─", colName) + "┼" + safeRepeat("─", colStatus) + "┼" + safeRepeat("─", colType) + "┼" + safeRepeat("─", colPool) + "┼" + safeRepeat("─", colUptime) + "┤" + reset + "\n"

		// Calculate how many jobs can be displayed
		// Chrome lines breakdown:
		// Before table: 1 (leading \n) + 4 (header box + \n) + 2 (summary + blank) + 3 (table header) = 10
		// After table: 1 (table bottom) + 2 (nav hint) + 1 (separator) + 1 (footer) = 5
		// Total chrome = 15 lines
		chromeLines := 15
		maxVisibleJobs := len(filteredJobs)
		if m.height > 0 {
			maxVisibleJobs = m.height - chromeLines
			if maxVisibleJobs < 1 {
				maxVisibleJobs = 1
			}
		}

		// Clamp scroll offset
		maxScroll := len(filteredJobs) - maxVisibleJobs
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
		if endIndex > len(filteredJobs) {
			endIndex = len(filteredJobs)
		}

		for i := m.jobsScrollOffset; i < endIndex; i++ {
			job := filteredJobs[i]

			// Job name with selection highlight and filter highlighting
			name := truncate(job.Name, colName-2)
			var nameField string
			_, isMultiSelected := m.selectedJobIDs[job.ID]
			if i == m.selectedIndex {
				// Cursor position - use primary highlight
				nameField = bold + m.theme.HighlightBg + "\033[30m" + fmt.Sprintf(" %-*s", colName-1, name) + reset
			} else if isMultiSelected {
				// Multi-selected but not cursor - use secondary highlight (dimmed blue)
				nameField = "\033[48;5;17m\033[37m" + fmt.Sprintf(" %-*s", colName-1, name) + reset
			} else {
				// Apply filter highlighting if filter is active
				if m.filterInput != "" {
					name = highlightMatch(name, m.filterInput, m.theme.Running, reset)
				}
				nameField = " " + white + name + safeRepeat(" ", colName-1-len(truncate(job.Name, colName-2))) + reset
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

		content += "  " + dimmed + "╰" + safeRepeat("─", colName) + "┴" + safeRepeat("─", colStatus) + "┴" + safeRepeat("─", colType) + "┴" + safeRepeat("─", colPool) + "┴" + safeRepeat("─", colUptime) + "╯" + reset + "\n"

		// Scroll indicator (if list is scrollable)
		scrollIndicator := ""
		if len(filteredJobs) > maxVisibleJobs {
			scrollIndicator = fmt.Sprintf("  %s(%d-%d of %d)%s", dimmed, m.jobsScrollOffset+1, endIndex, len(filteredJobs), reset)
		}

		// Selected job info footer (above navigation)
		if m.selectedIndex >= 0 && m.selectedIndex < len(filteredJobs) {
			selectedJob := filteredJobs[m.selectedIndex]
			if selectedJob != nil {
				var jobInfo string
				if selectedJob.Status == "running" && len(selectedJob.allocs) > 0 {
					// Show CPU/Memory for running jobs with visual indicators
					cpuStr := fmt.Sprintf("%.0f MHz", selectedJob.avgCPU)
					memStr := fmt.Sprintf("%.0f MB", selectedJob.avgMem)
					allocCount := len(selectedJob.allocs)
					jobInfo = fmt.Sprintf("%s%s%s  │  %s%s⚡%s%s CPU:%s %s%s%s  │  %s%s▣%s%s Memory:%s %s%s%s  │  %s%s■%s%s Allocations:%s %s%d%s",
						bold, selectedJob.Name, reset,
						dimmed, m.theme.Running, reset, dimmed, reset, m.theme.Running, cpuStr, reset,
						dimmed, cyan, reset, dimmed, reset, cyan, memStr, reset,
						dimmed, m.theme.Running, reset, dimmed, reset, bold, allocCount, reset)
				} else {
					// Show allocation status breakdown for dead/batch jobs
					statusInfo := ""
					for status, count := range selectedJob.allocStatuses {
						if statusInfo != "" {
							statusInfo += "  │  "
						}
						statusColor := ansiColor(status, m.theme)
						statusInfo += fmt.Sprintf("%s: %s%d%s", status, statusColor, count, reset)
					}
					if statusInfo == "" {
						statusInfo = dimmed + "No allocations" + reset
					}
					jobInfo = fmt.Sprintf("%s%s%s  │  Type: %s  │  %s",
						bold, selectedJob.Name, reset,
						selectedJob.Type,
						statusInfo)
				}
				// Center the job info
				jobInfoLen := len(stripAnsi(jobInfo))
				padding := ""
				if m.width > jobInfoLen {
					leftPad := (m.width - jobInfoLen) / 2
					padding = strings.Repeat(" ", leftPad)
				}
				content += "\n"
				content += padding + jobInfo + "\n"
				content += "  " + dimmed + strings.Repeat("─", m.width-4) + reset + "\n\n"
			}
		}

		// Navigation hint with filter indicator
		filterHint := ""
		if m.filterInput != "" {
			filterHint = "  " + dimmed + "│" + reset + "  " + m.theme.Running + "/" + reset + " Filter: " + bold + m.filterInput + reset
		} else {
			filterHint = "  " + dimmed + "│" + reset + "  " + cyan + "/" + reset + " Filter"
		}
		content += "  " + dimmed + "↑↓" + reset + " Navigate  " + dimmed + "│" + reset + "  " + dimmed + "Shift+↑↓" + reset + " Multi-select  " + dimmed + "│" + reset + "  " + cyan + "Enter" + reset + " Details  " + dimmed + "│" + reset + "  " + cyan + "s" + reset + " Stop  " + dimmed + "│" + reset + "  " + cyan + "d" + reset + " Delete" + filterHint + "  " + scrollIndicator + "\n"

	case "nodes":
		// Header
		content = "\n" + renderHeader("🖥️  NOMAD NODES", boxWidth, m.theme.Header, bold, reset) + "\n"

		// Filter nodes if filter is active
		filteredNodes := m.nodes
		if m.filterInput != "" {
			filteredNodes = make([]*nodeStats, 0)
			for _, node := range m.nodes {
				if matchesFilter(node.Name, m.filterInput) || matchesFilter(node.ID, m.filterInput) {
					filteredNodes = append(filteredNodes, node)
				}
			}
		}

		// Summary bar
		readyCount := 0
		downCount := 0
		for _, node := range filteredNodes {
			if node.Status == "ready" {
				readyCount++
			} else {
				downCount++
			}
		}
		content += "  " + dimmed + "Total:" + reset + " " + bold + fmt.Sprintf("%d", len(filteredNodes)) + reset
		if m.filterInput != "" {
			content += " " + dimmed + "(filtered from " + fmt.Sprintf("%d", len(m.nodes)) + ")" + reset
		}
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
		content += "  " + dimmed + "╭" + safeRepeat("─", colID) + "┬" + safeRepeat("─", colName) + "┬" + safeRepeat("─", colDC) + "┬" + safeRepeat("─", colOS) + "┬" + safeRepeat("─", colStatus) + "┬" + safeRepeat("─", colVersion) + "┬" + safeRepeat("─", colIP) + "╮" + reset + "\n"
		content += "  " + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colID-1, "ID") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colName-1, "Name") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colDC-1, "DC") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colOS-1, "OS") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colStatus-1, "Status") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colVersion-1, "Version") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colIP-1, "IP") + reset + dimmed + "│" + reset + "\n"
		content += "  " + dimmed + "├" + safeRepeat("─", colID) + "┼" + safeRepeat("─", colName) + "┼" + safeRepeat("─", colDC) + "┼" + safeRepeat("─", colOS) + "┼" + safeRepeat("─", colStatus) + "┼" + safeRepeat("─", colVersion) + "┼" + safeRepeat("─", colIP) + "┤" + reset + "\n"

		// Calculate how many nodes can be displayed
		// Chrome lines breakdown: same as jobs = 15 lines
		chromeLines := 15
		maxVisibleNodes := len(filteredNodes)
		if m.height > 0 {
			maxVisibleNodes = m.height - chromeLines
			if maxVisibleNodes < 1 {
				maxVisibleNodes = 1
			}
		}

		// Clamp scroll offset
		maxScroll := len(filteredNodes) - maxVisibleNodes
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
		if endIndex > len(filteredNodes) {
			endIndex = len(filteredNodes)
		}

		for i := m.nodesScrollOffset; i < endIndex; i++ {
			node := filteredNodes[i]

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
				// Apply filter highlighting if filter is active
				if m.filterInput != "" {
					idText = highlightMatch(idText, m.filterInput, m.theme.Running, reset)
				}
				idField = " " + idText + safeRepeat(" ", colID-1-len(truncate(node.ID, colID-2))) + reset
			}

			// DC
			dcField := " " + fmt.Sprintf("%-*s", colDC-1, truncate(node.datacenter, colDC-2))

			// Name with filter highlighting
			nodeName := truncate(node.Name, colName-2)
			nameField := ""
			if m.filterInput != "" && i != m.selectedNodeIndex {
				nodeName = highlightMatch(nodeName, m.filterInput, m.theme.Running, reset)
				nameField = " " + nodeName + safeRepeat(" ", colName-1-len(truncate(node.Name, colName-2)))
			} else {
				nameField = " " + fmt.Sprintf("%-*s", colName-1, nodeName)
			}

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

		content += "  " + dimmed + "╰" + safeRepeat("─", colID) + "┴" + safeRepeat("─", colName) + "┴" + safeRepeat("─", colDC) + "┴" + safeRepeat("─", colOS) + "┴" + safeRepeat("─", colStatus) + "┴" + safeRepeat("─", colVersion) + "┴" + safeRepeat("─", colIP) + "╯" + reset + "\n"

		// Scroll indicator (if list is scrollable)
		scrollIndicator := ""
		if len(filteredNodes) > maxVisibleNodes {
			scrollIndicator = fmt.Sprintf("  %s(%d-%d of %d)%s", dimmed, m.nodesScrollOffset+1, endIndex, len(filteredNodes), reset)
		}

		// Navigation hint with filter indicator
		filterHint := ""
		if m.filterInput != "" {
			filterHint = "  " + dimmed + "│" + reset + "  " + m.theme.Running + "/" + reset + " Filter: " + bold + m.filterInput + reset
		} else {
			filterHint = "  " + dimmed + "│" + reset + "  " + cyan + "/" + reset + " Filter"
		}
		content += "\n  " + dimmed + "↑↓" + reset + " Navigate  " + dimmed + "│" + reset + "  " + dimmed + "←→" + reset + " Switch View  " + dimmed + "│" + reset + "  " + cyan + "Enter" + reset + " Details" + filterHint + "  " + scrollIndicator + "\n"

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
		content += "  " + dimmed + "╭" + safeRepeat("─", colLabel) + "┬" + safeRepeat("─", colValue) + "┬" + safeRepeat("─", colStatus) + "╮" + reset + "\n"
		content += "  " + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colLabel-1, "Resource") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colValue-1, "Total") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colStatus-1, "Status") + reset + dimmed + "│" + reset + "\n"
		content += "  " + dimmed + "├" + safeRepeat("─", colLabel) + "┼" + safeRepeat("─", colValue) + "┼" + safeRepeat("─", colStatus) + "┤" + reset + "\n"

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
		content += "  " + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colLabel-1, "Nodes") + dimmed + "│" + reset + fmt.Sprintf(" %-*d", colValue-1, len(m.nodes)) + dimmed + "│" + reset + " " + nodesStatus + safeRepeat(" ", nodesPadding) + dimmed + "│" + reset + "\n"

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
		content += "  " + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colLabel-1, "Jobs") + dimmed + "│" + reset + fmt.Sprintf(" %-*d", colValue-1, len(m.jobs)) + dimmed + "│" + reset + " " + jobsStatus + safeRepeat(" ", jobsPadding) + dimmed + "│" + reset + "\n"

		content += "  " + dimmed + "╰" + safeRepeat("─", colLabel) + "┴" + safeRepeat("─", colValue) + "┴" + safeRepeat("─", colStatus) + "╯" + reset + "\n\n"

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
		content += "  " + dimmed + "╭" + safeRepeat("─", colResource) + "┬" + safeRepeat("─", colCapacity) + "┬" + safeRepeat("─", colAllocated) + "┬" + safeRepeat("─", colAvailable) + "┬" + safeRepeat("─", colUtil) + "╮" + reset + "\n"
		content += "  " + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colResource-1, "Resource") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colCapacity-1, "Capacity") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colAllocated-1, "Allocated") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colAvailable-1, "Available") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colUtil-1, "Used") + reset + dimmed + "│" + reset + "\n"
		content += "  " + dimmed + "├" + safeRepeat("─", colResource) + "┼" + safeRepeat("─", colCapacity) + "┼" + safeRepeat("─", colAllocated) + "┼" + safeRepeat("─", colAvailable) + "┼" + safeRepeat("─", colUtil) + "┤" + reset + "\n"

		// CPU row
		cpuUtilStr := fmt.Sprintf("%.1f%%", utilCPU)
		content += "  " + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colResource-1, "CPU") + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colCapacity-1, fmt.Sprintf("%.1f GHz", capacityGHz)) + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colAllocated-1, fmt.Sprintf("%.1f GHz", allocatedGHz)) + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colAvailable-1, fmt.Sprintf("%.1f GHz", availableGHz)) + dimmed + "│" + reset + " " + cpuColor + fmt.Sprintf("%-*s", colUtil-1, cpuUtilStr) + reset + dimmed + "│" + reset + "\n"

		// Memory row
		memUtilStr := fmt.Sprintf("%.1f%%", utilMem)
		content += "  " + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colResource-1, "Memory") + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colCapacity-1, fmt.Sprintf("%.1f GB", capacityGB)) + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colAllocated-1, fmt.Sprintf("%.1f GB", allocatedGB)) + dimmed + "│" + reset + fmt.Sprintf(" %-*s", colAvailable-1, fmt.Sprintf("%.1f GB", availableGB)) + dimmed + "│" + reset + " " + memColor + fmt.Sprintf("%-*s", colUtil-1, memUtilStr) + reset + dimmed + "│" + reset + "\n"

		content += "  " + dimmed + "╰" + safeRepeat("─", colResource) + "┴" + safeRepeat("─", colCapacity) + "┴" + safeRepeat("─", colAllocated) + "┴" + safeRepeat("─", colAvailable) + "┴" + safeRepeat("─", colUtil) + "╯" + reset + "\n\n"

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
		cpuBar := cpuColor + safeRepeat("█", filledWidth) + reset + dimmed + safeRepeat("░", barWidth-filledWidth) + reset
		content += "  " + dimmed + "CPU" + reset + "     " + cpuBar + " " + cpuColor + fmt.Sprintf("%5.1f%%", utilCPU) + reset + "\n"

		// Memory Progress bar
		memFilledWidth := int(utilMem / 100 * float64(barWidth))
		if memFilledWidth > barWidth {
			memFilledWidth = barWidth
		}
		memBar := memColor + safeRepeat("█", memFilledWidth) + reset + dimmed + safeRepeat("░", barWidth-memFilledWidth) + reset
		content += "  " + dimmed + "Memory" + reset + "  " + memBar + " " + memColor + fmt.Sprintf("%5.1f%%", utilMem) + reset + "\n"

		// Navigation hint
		content += "\n  " + dimmed + "↑↓" + reset + " Scroll  " + dimmed + "│" + reset + "  " + dimmed + "←→" + reset + " Switch View  " + dimmed + "│" + reset + "  " + dimmed + "(more content below)" + reset + "\n"

	case "services":
		// Header
		content = "\n" + renderHeader("🔗 NOMAD SERVICES", boxWidth, m.theme.Header, bold, reset) + "\n"

		// Filter services if filter is active
		filteredServices := m.services
		if m.filterInput != "" {
			filteredServices = make([]*serviceInfo, 0)
			for _, svc := range m.services {
				if matchesFilter(svc.Name, m.filterInput) {
					filteredServices = append(filteredServices, svc)
				}
			}
		}

		// Summary bar
		content += "  " + dimmed + "Total:" + reset + " " + bold + fmt.Sprintf("%d", len(filteredServices)) + reset + " services registered"
		if m.filterInput != "" {
			content += " " + dimmed + "(filtered from " + fmt.Sprintf("%d", len(m.services)) + ")" + reset
		}
		content += "\n\n"

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
		content += "  " + dimmed + "╭" + safeRepeat("─", colName) + "┬" + safeRepeat("─", colTags) + "┬" + safeRepeat("─", colAddress) + "┬" + safeRepeat("─", colPort) + "┬" + safeRepeat("─", colJob) + "┬" + safeRepeat("─", colNode) + "╮" + reset + "\n"
		content += "  " + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colName-1, "Service Name") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colTags-1, "Tags") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colAddress-1, "Address") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colPort-1, "Port") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colJob-1, "Job") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colNode-1, "Node") + reset + dimmed + "│" + reset + "\n"
		content += "  " + dimmed + "├" + safeRepeat("─", colName) + "┼" + safeRepeat("─", colTags) + "┼" + safeRepeat("─", colAddress) + "┼" + safeRepeat("─", colPort) + "┼" + safeRepeat("─", colJob) + "┼" + safeRepeat("─", colNode) + "┤" + reset + "\n"

		// Calculate how many services can be displayed
		// Chrome lines breakdown: same as jobs = 15 lines
		chromeLines := 15
		maxVisibleServices := len(filteredServices)
		if m.height > 0 {
			maxVisibleServices = m.height - chromeLines
			if maxVisibleServices < 1 {
				maxVisibleServices = 1
			}
		}

		// Clamp scroll offset
		maxScroll := len(filteredServices) - maxVisibleServices
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
		if endIndex > len(filteredServices) {
			endIndex = len(filteredServices)
		}

		if len(filteredServices) == 0 {
			// Empty state
			emptyMsg := "No services registered"
			if m.filterInput != "" {
				emptyMsg = "No services match filter"
			}
			emptyPadding := (colName + colTags + colAddress + colPort + colJob + colNode + 5 - len(emptyMsg)) / 2
			content += "  " + dimmed + "│" + reset + safeRepeat(" ", emptyPadding) + dimmed + emptyMsg + reset + safeRepeat(" ", colName+colTags+colAddress+colPort+colJob+colNode+5-emptyPadding-len(emptyMsg)) + dimmed + "│" + reset + "\n"
		} else {
			for i := m.servicesScrollOffset; i < endIndex; i++ {
				svc := filteredServices[i]

				// Service name with selection highlight and filter highlighting
				name := truncate(svc.Name, colName-2)
				var nameField string
				if i == m.selectedServiceIndex {
					nameField = bold + m.theme.HighlightBg + "\033[30m" + fmt.Sprintf(" %-*s", colName-1, name) + reset
				} else {
					// Apply filter highlighting if filter is active
					if m.filterInput != "" {
						name = highlightMatch(name, m.filterInput, m.theme.Running, reset)
					}
					nameField = " " + white + name + safeRepeat(" ", colName-1-len(truncate(svc.Name, colName-2))) + reset
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

		content += "  " + dimmed + "╰" + safeRepeat("─", colName) + "┴" + safeRepeat("─", colTags) + "┴" + safeRepeat("─", colAddress) + "┴" + safeRepeat("─", colPort) + "┴" + safeRepeat("─", colJob) + "┴" + safeRepeat("─", colNode) + "╯" + reset + "\n"

		// Scroll indicator (if list is scrollable)
		scrollIndicator := ""
		if len(filteredServices) > maxVisibleServices {
			scrollIndicator = fmt.Sprintf("  %s(%d-%d of %d)%s", dimmed, m.servicesScrollOffset+1, endIndex, len(filteredServices), reset)
		}

		// Navigation hint with filter indicator
		filterHint := ""
		if m.filterInput != "" {
			filterHint = "  " + dimmed + "│" + reset + "  " + m.theme.Running + "/" + reset + " Filter: " + bold + m.filterInput + reset
		} else {
			filterHint = "  " + dimmed + "│" + reset + "  " + cyan + "/" + reset + " Filter"
		}
		content += "\n  " + dimmed + "↑↓" + reset + " Navigate  " + dimmed + "│" + reset + "  " + dimmed + "←→" + reset + " Switch View" + filterHint + "  " + scrollIndicator + "\n"

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
			content += "  " + dimmed + safeRepeat("─", separatorWidth) + reset + "\n"
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
			content += "  " + dimmed + safeRepeat("─", separatorWidth) + reset + "\n"

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
					cpuBar := cpuColor + safeRepeat("█", filledWidth) + reset + dimmed + safeRepeat("░", barWidth-filledWidth) + reset
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
					memBar := memColor + safeRepeat("█", filledWidth) + reset + dimmed + safeRepeat("░", barWidth-filledWidth) + reset
					content += "  " + dimmed + "Mem:" + reset + "  " + memBar + fmt.Sprintf(" %d / %d MB", allocatedMemMB, totalMemMB) + "\n"
				}
			} else {
				content += "  " + dimmed + "Unable to fetch resource details" + reset + "\n"
			}

			content += "\n"

			// Drivers & Volumes Section
			content += "  " + bold + cyan + "CAPABILITIES" + reset + "\n"
			content += "  " + dimmed + safeRepeat("─", separatorWidth) + reset + "\n"
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
			content += "  " + dimmed + "╭" + safeRepeat("─", cardWidth) + "╮" + reset + gap
			content += dimmed + "╭" + safeRepeat("─", cardWidth) + "╮" + reset + "\n"

			// Card headers
			content += "  " + dimmed + "│" + reset + " " + bold + cyan + "CONFIGURATION" + reset + safeRepeat(" ", cardWidth-14) + dimmed + "│" + reset + gap
			content += dimmed + "│" + reset + " " + bold + cyan + "RESOURCES" + reset + safeRepeat(" ", cardWidth-10) + dimmed + "│" + reset + "\n"

			// Separator
			content += "  " + dimmed + "├" + safeRepeat("─", cardWidth) + "┤" + reset + gap
			content += dimmed + "├" + safeRepeat("─", cardWidth) + "┤" + reset + "\n"

			// Row 1: Type | CPU
			content += "  " + dimmed + "│" + reset + cardRow("Type", selectedJob.Type) + dimmed + "│" + reset + gap
			content += dimmed + "│" + reset + cardRow("CPU", fmt.Sprintf("%d MHz", totalCPU)) + dimmed + "│" + reset + "\n"

			// Row 2: Node Pool | Memory
			content += "  " + dimmed + "│" + reset + cardRow("Node Pool", selectedJob.nodePool) + dimmed + "│" + reset + gap
			content += dimmed + "│" + reset + cardRow("Memory", fmt.Sprintf("%d MB", totalMem)) + dimmed + "│" + reset + "\n"

			// Row 3: Uptime | (empty padding)
			content += "  " + dimmed + "│" + reset + cardRow("Uptime", durationStr) + dimmed + "│" + reset + gap
			content += dimmed + "│" + reset + safeRepeat(" ", cardWidth) + dimmed + "│" + reset + "\n"

			// Bottom border
			content += "  " + dimmed + "╰" + safeRepeat("─", cardWidth) + "╯" + reset + gap
			content += dimmed + "╰" + safeRepeat("─", cardWidth) + "╯" + reset + "\n\n"

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

			// Filter allocations if filter is active and in alloc select mode
			filteredAllocs := selectedJob.allocs
			if m.filterInput != "" && m.allocSelectMode {
				filteredAllocs = make([]*api.AllocationListStub, 0)
				for _, alloc := range selectedJob.allocs {
					if matchesFilter(alloc.ID, m.filterInput) || matchesFilter(alloc.TaskGroup, m.filterInput) {
						filteredAllocs = append(filteredAllocs, alloc)
					}
				}
			}

			if len(filteredAllocs) > 0 {
				// Calculate dynamic column widths for allocations table
				// 5 columns: ID, TaskGroup, Status, Event, Node
				allocTableWidth := getTableWidth(m.width, 5)
				allocColWidths := calculateColumnWidths(allocTableWidth, []int{2, 3, 2, 2, 3}, []int{10, 12, 10, 10, 12})
				colID := allocColWidths[0]
				colTaskGroup := allocColWidths[1]
				colStatus := allocColWidths[2]
				colEvent := allocColWidths[3]
				colNode := allocColWidths[4]

				content += "  " + dimmed + "╭" + safeRepeat("─", colID) + "┬" + safeRepeat("─", colTaskGroup) + "┬" + safeRepeat("─", colStatus) + "┬" + safeRepeat("─", colEvent) + "┬" + safeRepeat("─", colNode) + "╮" + reset + "\n"
				content += "  " + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colID-1, "Alloc ID") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colTaskGroup-1, "Task Group") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colStatus-1, "Status") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colEvent-1, "Last Event") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colNode-1, "Node") + reset + dimmed + "│" + reset + "\n"
				content += "  " + dimmed + "├" + safeRepeat("─", colID) + "┼" + safeRepeat("─", colTaskGroup) + "┼" + safeRepeat("─", colStatus) + "┼" + safeRepeat("─", colEvent) + "┼" + safeRepeat("─", colNode) + "┤" + reset + "\n"

				maxAllocs := len(filteredAllocs)

				for i := 0; i < maxAllocs; i++ {
					alloc := filteredAllocs[i]
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

					// Task Group with filter highlighting
					taskGroup := truncate(alloc.TaskGroup, colTaskGroup-2)
					taskGroupField := ""
					if m.filterInput != "" && m.allocSelectMode && i != m.selectedAllocIndex {
						taskGroup = highlightMatch(taskGroup, m.filterInput, m.theme.Running, reset)
						taskGroupField = " " + taskGroup + safeRepeat(" ", colTaskGroup-1-len(truncate(alloc.TaskGroup, colTaskGroup-2)))
					} else {
						taskGroupField = fmt.Sprintf(" %-*s", colTaskGroup-1, taskGroup)
					}

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

					// Format ID field with filter highlighting
					allocIDDisplay := allocID
					idField := ""
					if m.filterInput != "" && m.allocSelectMode && i != m.selectedAllocIndex {
						allocIDDisplay = highlightMatch(allocIDDisplay, m.filterInput, m.theme.Running, reset)
						idField = " " + allocIDDisplay + safeRepeat(" ", colID-1-len(allocID))
					} else {
						idField = fmt.Sprintf(" %-*s", colID-1, allocID)
					}
					// Build status field manually for correct visual width
					// colStatus = 12: 1 leading space + 1 icon (visual) + 1 space + status + trailing padding
					// Visual width needed for status + padding = 12 - 3 = 9
					statusText := statusIcon + " " + status
					visualWidth := 1 + 1 + len(status) // icon(1) + space(1) + status
					statusPadding := colStatus - 1 - visualWidth
					if statusPadding < 0 {
						statusPadding = 0
					}
					statusField := " " + statusText + safeRepeat(" ", statusPadding)
					eventField := fmt.Sprintf(" %-*s", colEvent-1, truncate(lastEvent, colEvent-2))
					nodeField := fmt.Sprintf(" %-*s", colNode-1, nodeName)

					if i == m.selectedAllocIndex {
						content += "  " + dimmed + "│" + reset + bold + m.theme.HighlightBg + "\033[30m" + idField + reset + dimmed + "│" + reset + taskGroupField + dimmed + "│" + reset + ansiColor(status, m.theme) + statusField + reset + dimmed + "│" + reset + eventField + dimmed + "│" + reset + nodeField + dimmed + "│" + reset + "\n"
					} else {
						content += "  " + dimmed + "│" + reset + idField + dimmed + "│" + reset + taskGroupField + dimmed + "│" + reset + ansiColor(status, m.theme) + statusField + reset + dimmed + "│" + reset + eventField + dimmed + "│" + reset + nodeField + dimmed + "│" + reset + "\n"
					}
				}

				content += "  " + dimmed + "╰" + safeRepeat("─", colID) + "┴" + safeRepeat("─", colTaskGroup) + "┴" + safeRepeat("─", colStatus) + "┴" + safeRepeat("─", colEvent) + "┴" + safeRepeat("─", colNode) + "╯" + reset + "\n"
			} else {
				content += "  " + dimmed + "No allocations" + reset + "\n"
			}

			// Evaluations section
			content += "\n  " + bold + cyan + "EVALUATIONS" + reset + "\n"

			// Filter evaluations if filter is active and in eval select mode
			filteredEvals := selectedJob.evaluations
			if m.filterInput != "" && m.evalSelectMode {
				filteredEvals = make([]*api.Evaluation, 0)
				for _, eval := range selectedJob.evaluations {
					if matchesFilter(eval.ID, m.filterInput) || matchesFilter(eval.TriggeredBy, m.filterInput) {
						filteredEvals = append(filteredEvals, eval)
					}
				}
			}

			if len(filteredEvals) > 0 {
				// Calculate dynamic column widths for evaluations table
				// 5 columns: EvalID, Status, TriggeredBy, Placement, Time
				evalTableWidth := getTableWidth(m.width, 5)
				evalColWidths := calculateColumnWidths(evalTableWidth, []int{2, 2, 2, 3, 2}, []int{10, 10, 12, 14, 14})
				colEvalID := evalColWidths[0]
				colEvalStatus := evalColWidths[1]
				colTriggeredBy := evalColWidths[2]
				colPlacement := evalColWidths[3]
				colTime := evalColWidths[4]

				content += "  " + dimmed + "╭" + safeRepeat("─", colEvalID) + "┬" + safeRepeat("─", colEvalStatus) + "┬" + safeRepeat("─", colTriggeredBy) + "┬" + safeRepeat("─", colPlacement) + "┬" + safeRepeat("─", colTime) + "╮" + reset + "\n"
				content += "  " + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colEvalID-1, "Eval ID") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colEvalStatus-1, "Status") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colTriggeredBy-1, "Triggered By") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colPlacement-1, "Placement") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colTime-1, "Time") + reset + dimmed + "│" + reset + "\n"
				content += "  " + dimmed + "├" + safeRepeat("─", colEvalID) + "┼" + safeRepeat("─", colEvalStatus) + "┼" + safeRepeat("─", colTriggeredBy) + "┼" + safeRepeat("─", colPlacement) + "┼" + safeRepeat("─", colTime) + "┤" + reset + "\n"

				// Show up to 5 most recent evaluations (or all if filtered)
				maxEvals := 5
				if m.filterInput != "" && m.evalSelectMode {
					maxEvals = len(filteredEvals)
				} else if len(filteredEvals) < maxEvals {
					maxEvals = len(filteredEvals)
				}

				for i := 0; i < maxEvals; i++ {
					eval := filteredEvals[i]

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

					// Triggered by with filter highlighting
					triggeredBy := truncate(eval.TriggeredBy, colTriggeredBy-2)
					triggeredField := ""
					if m.filterInput != "" && m.evalSelectMode && i != m.selectedEvalIndex {
						triggeredBy = highlightMatch(triggeredBy, m.filterInput, m.theme.Running, reset)
						triggeredField = " " + triggeredBy + safeRepeat(" ", colTriggeredBy-1-len(truncate(eval.TriggeredBy, colTriggeredBy-2)))
					} else {
						triggeredField = fmt.Sprintf(" %-*s", colTriggeredBy-1, triggeredBy)
					}

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

					// Format fields with filter highlighting
					idField := ""
					if m.filterInput != "" && m.evalSelectMode && i != m.selectedEvalIndex {
						evalIDHighlighted := highlightMatch(evalID, m.filterInput, m.theme.Running, reset)
						idField = " " + evalIDHighlighted + safeRepeat(" ", colEvalID-1-len(evalID))
					} else {
						idField = fmt.Sprintf(" %-*s", colEvalID-1, evalID)
					}
					statusField := fmt.Sprintf(" %-*s", colEvalStatus-1, evalStatus)
					placementField := fmt.Sprintf(" %-*s", colPlacement-1, placementStatus)
					timeField := fmt.Sprintf(" %-*s", colTime-1, timeStr)

					// Highlight selected evaluation when in eval select mode
					if i == m.selectedEvalIndex && m.evalSelectMode {
						content += "  " + dimmed + "│" + reset + bold + m.theme.HighlightBg + "\033[30m" + idField + reset + dimmed + "│" + reset + statusColor + statusField + reset + dimmed + "│" + reset + triggeredField + dimmed + "│" + reset + placementColor + placementField + reset + dimmed + "│" + reset + timeField + dimmed + "│" + reset + "\n"
					} else {
						content += "  " + dimmed + "│" + reset + idField + dimmed + "│" + reset + statusColor + statusField + reset + dimmed + "│" + reset + triggeredField + dimmed + "│" + reset + placementColor + placementField + reset + dimmed + "│" + reset + timeField + dimmed + "│" + reset + "\n"
					}
				}

				content += "  " + dimmed + "╰" + safeRepeat("─", colEvalID) + "┴" + safeRepeat("─", colEvalStatus) + "┴" + safeRepeat("─", colTriggeredBy) + "┴" + safeRepeat("─", colPlacement) + "┴" + safeRepeat("─", colTime) + "╯" + reset + "\n"
			} else {
				content += "  " + dimmed + "No evaluations" + reset + "\n"
			}

			// Placement failures section
			if selectedJob.hasPlacementIssues && len(selectedJob.placementFailures) > 0 {
				content += "\n  " + bold + m.theme.Dead + "⚠ PLACEMENT FAILURES" + reset + "\n"

				for taskGroup, metrics := range selectedJob.placementFailures {
					content += "  " + dimmed + "╭" + safeRepeat("─", 76) + "╮" + reset + "\n"
					content += "  " + dimmed + "│" + reset + " " + bold + "Task Group: " + reset + white + taskGroup + reset + safeRepeat(" ", 76-14-len(taskGroup)) + dimmed + "│" + reset + "\n"
					content += "  " + dimmed + "├" + safeRepeat("─", 76) + "┤" + reset + "\n"

					// Show evaluation summary
					evalInfo := fmt.Sprintf("Nodes Evaluated: %d  |  Nodes Filtered: %d  |  Nodes Exhausted: %d",
						metrics.NodesEvaluated, metrics.NodesFiltered, metrics.NodesExhausted)
					content += "  " + dimmed + "│" + reset + " " + fmt.Sprintf("%-75s", evalInfo) + dimmed + "│" + reset + "\n"

					// Show constraint failures if any
					if len(metrics.ConstraintFiltered) > 0 {
						content += "  " + dimmed + "│" + reset + " " + m.theme.Dead + "Constraint Failures:" + reset + safeRepeat(" ", 54) + dimmed + "│" + reset + "\n"
						for constraint, count := range metrics.ConstraintFiltered {
							constraintLine := fmt.Sprintf("  • %s (%d nodes filtered)", truncate(constraint, 60), count)
							content += "  " + dimmed + "│" + reset + " " + fmt.Sprintf("%-75s", constraintLine) + dimmed + "│" + reset + "\n"
						}
					}

					// Show dimension exhausted (resource shortages)
					if len(metrics.DimensionExhausted) > 0 {
						content += "  " + dimmed + "│" + reset + " " + m.theme.Dead + "Resource Exhaustion:" + reset + safeRepeat(" ", 54) + dimmed + "│" + reset + "\n"
						for dimension, count := range metrics.DimensionExhausted {
							dimLine := fmt.Sprintf("  • %s exhausted on %d nodes", dimension, count)
							content += "  " + dimmed + "│" + reset + " " + fmt.Sprintf("%-75s", dimLine) + dimmed + "│" + reset + "\n"
						}
					}

					// Show class exhausted if any
					if len(metrics.ClassExhausted) > 0 {
						content += "  " + dimmed + "│" + reset + " " + m.theme.Dead + "Node Class Exhausted:" + reset + safeRepeat(" ", 53) + dimmed + "│" + reset + "\n"
						for class, count := range metrics.ClassExhausted {
							classLine := fmt.Sprintf("  • %s (%d nodes)", class, count)
							content += "  " + dimmed + "│" + reset + " " + fmt.Sprintf("%-75s", classLine) + dimmed + "│" + reset + "\n"
						}
					}

					// Show quota exhausted if any
					if len(metrics.QuotaExhausted) > 0 {
						content += "  " + dimmed + "│" + reset + " " + m.theme.Dead + "Quota Exhausted:" + reset + safeRepeat(" ", 58) + dimmed + "│" + reset + "\n"
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

					content += "  " + dimmed + "╰" + safeRepeat("─", 76) + "╯" + reset + "\n"
				}
			}

			// Clean navigation bar - dynamic based on allocation/evaluation selection mode
			filterHint := ""
			if m.filterInput != "" && (m.allocSelectMode || m.evalSelectMode) {
				filterHint = "  " + dimmed + "│" + reset + "  " + m.theme.Running + "/" + reset + " Filter: " + bold + m.filterInput + reset
			} else if m.allocSelectMode || m.evalSelectMode {
				filterHint = "  " + dimmed + "│" + reset + "  " + cyan + "/" + reset + " Filter"
			}

			if m.allocSelectMode {
				content += "\n  " + m.theme.Running + "ALLOC MODE" + reset + "  " + dimmed + "│" + reset + "  " + dimmed + "↑↓" + reset + " Navigate  " + dimmed + "│" + reset + "  " + cyan + "Enter" + reset + " Details  " + dimmed + "│" + reset + "  " + cyan + "s" + reset + " Stop  " + dimmed + "│" + reset + "  " + cyan + "x" + reset + " Restart  " + dimmed + "│" + reset + "  " + cyan + "l" + reset + " Logs" + filterHint + "  " + dimmed + "│" + reset + "  " + cyan + "Esc" + reset + " Exit Mode\n"
			} else if m.evalSelectMode {
				content += "\n  " + m.theme.Pending + "EVAL MODE" + reset + "  " + dimmed + "│" + reset + "  " + dimmed + "↑↓" + reset + " Navigate  " + dimmed + "│" + reset + "  " + cyan + "Enter" + reset + " Details" + filterHint + "  " + dimmed + "│" + reset + "  " + cyan + "Esc" + reset + " Exit Mode\n"
			} else {
				content += "\n  " + dimmed + "↑↓" + reset + " Scroll  " + dimmed + "│" + reset + "  " + cyan + "a" + reset + " Select Alloc  " + dimmed + "│" + reset + "  " + cyan + "e" + reset + " Select Eval  " + dimmed + "│" + reset + "  " + cyan + "n" + reset + "/" + cyan + "p" + reset + " Next/Prev  " + dimmed + "│" + reset + "  " + cyan + "l" + reset + " Logs  " + dimmed + "│" + reset + "  " + cyan + "Esc" + reset + " Back\n"
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
		content += "  " + dimmed + safeRepeat("─", 60) + reset + "\n"

		// Calculate available lines for logs
		maxLogLines := m.height - 12
		if maxLogLines < 5 {
			maxLogLines = 5
		}

		// Split log content into lines
		logLines := strings.Split(m.logContent, "\n")
		totalLines := len(logLines)

		// Clamp scroll offset
		maxScroll := totalLines - maxLogLines
		if maxScroll < 0 {
			maxScroll = 0
		}
		if m.logsScrollOffset > maxScroll {
			m.logsScrollOffset = maxScroll
		}
		if m.logsScrollOffset < 0 {
			m.logsScrollOffset = 0
		}

		// Calculate visible range
		startLine := m.logsScrollOffset
		endLine := startLine + maxLogLines
		if endLine > totalLines {
			endLine = totalLines
		}

		for i := startLine; i < endLine; i++ {
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

		// Scroll indicator
		scrollIndicator := ""
		followIndicator := ""
		if m.logsFollowMode {
			followIndicator = fmt.Sprintf("  %s[FOLLOW]%s", m.theme.Running, reset)
		}
		if totalLines > maxLogLines {
			scrollIndicator = fmt.Sprintf("  %s(lines %d-%d of %d)%s", dimmed, startLine+1, endLine, totalLines, reset)
		}

		// Navigation hint
		content += "\n  " + dimmed + "↑↓" + reset + " Scroll  " + dimmed + "│" + reset + "  " + cyan + "Home" + reset + " Top  " + dimmed + "│" + reset + "  " + cyan + "End" + reset + " Bottom  " + dimmed + "│" + reset + "  " + cyan + "p" + reset + " Pause/Follow  " + dimmed + "│" + reset + "  " + cyan + "r" + reset + " Refresh  " + dimmed + "│" + reset + "  " + cyan + "Esc" + reset + " Back" + followIndicator + scrollIndicator + "\n"

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

			content += "  " + dimmed + "╭" + safeRepeat("─", colTime) + "┬" + safeRepeat("─", colAlloc) + "┬" + safeRepeat("─", colTask) + "┬" + safeRepeat("─", colType) + "┬" + safeRepeat("─", colMsg) + "╮" + reset + "\n"
			content += "  " + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colTime-1, "Time") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colAlloc-1, "Alloc") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colTask-1, "Task") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colType-1, "Type") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colMsg-1, "Message") + reset + dimmed + "│" + reset + "\n"
			content += "  " + dimmed + "├" + safeRepeat("─", colTime) + "┼" + safeRepeat("─", colAlloc) + "┼" + safeRepeat("─", colTask) + "┼" + safeRepeat("─", colType) + "┼" + safeRepeat("─", colMsg) + "┤" + reset + "\n"

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

			content += "  " + dimmed + "╰" + safeRepeat("─", colTime) + "┴" + safeRepeat("─", colAlloc) + "┴" + safeRepeat("─", colTask) + "┴" + safeRepeat("─", colType) + "┴" + safeRepeat("─", colMsg) + "╯" + reset + "\n"
		}

		// Navigation hint
		content += "\n  " + dimmed + "↑↓" + reset + " Navigate  " + dimmed + "│" + reset + "  " + cyan + "r" + reset + " Refresh  " + dimmed + "│" + reset + "  " + cyan + "Esc" + reset + " Back\n"

	case "alloc-detail":
		// Header
		content = "\n" + renderHeader("📦 ALLOCATION DETAILS", boxWidth, m.theme.Header, bold, reset) + "\n"

		if m.selectedAlloc == nil {
			content += "  " + dimmed + "No allocation selected" + reset + "\n"
		} else {
			alloc := m.selectedAlloc

			// Allocation ID and status
			allocID := alloc.ID
			if len(allocID) > 16 {
				allocID = allocID[:16] + "..."
			}
			statusIcon := "●"
			statusLabel := strings.ToUpper(alloc.ClientStatus)
			statusColor := ansiColor(alloc.ClientStatus, m.theme)

			content += "  " + bold + white + allocID + reset + "  "
			content += statusColor + statusIcon + " " + statusLabel + reset + "\n"
			content += "  " + dimmed + "Job: " + alloc.JobID + " · Task Group: " + alloc.TaskGroup + reset + "\n\n"

			// Info cards
			cardWidth := 38
			gap := "  "

			cardRow := func(label string, value string) string {
				labelPadded := fmt.Sprintf("%-12s", label)
				valuePadded := fmt.Sprintf("%-*s", cardWidth-15, truncate(value, cardWidth-15))
				return "  " + dimmed + labelPadded + reset + " " + valuePadded
			}

			// Top border
			content += "  " + dimmed + "╭" + safeRepeat("─", cardWidth) + "╮" + reset + gap
			content += dimmed + "╭" + safeRepeat("─", cardWidth) + "╮" + reset + "\n"

			// Card headers
			content += "  " + dimmed + "│" + reset + " " + bold + cyan + "ALLOCATION INFO" + reset + safeRepeat(" ", cardWidth-16) + dimmed + "│" + reset + gap
			content += dimmed + "│" + reset + " " + bold + cyan + "RESOURCES" + reset + safeRepeat(" ", cardWidth-10) + dimmed + "│" + reset + "\n"

			// Separator
			content += "  " + dimmed + "├" + safeRepeat("─", cardWidth) + "┤" + reset + gap
			content += dimmed + "├" + safeRepeat("─", cardWidth) + "┤" + reset + "\n"

			// Row 1: Node | CPU
			nodeID := "N/A"
			if alloc.NodeID != "" {
				nodeID = alloc.NodeID[:8]
			}
			cpuMHz := 0
			if alloc.Resources != nil && alloc.Resources.CPU != nil {
				cpuMHz = *alloc.Resources.CPU
			}
			content += "  " + dimmed + "│" + reset + cardRow("Node", nodeID) + dimmed + "│" + reset + gap
			content += dimmed + "│" + reset + cardRow("CPU", fmt.Sprintf("%d MHz", cpuMHz)) + dimmed + "│" + reset + "\n"

			// Row 2: Desired | Memory
			memMB := 0
			if alloc.Resources != nil && alloc.Resources.MemoryMB != nil {
				memMB = *alloc.Resources.MemoryMB
			}
			content += "  " + dimmed + "│" + reset + cardRow("Desired", alloc.DesiredStatus) + dimmed + "│" + reset + gap
			content += dimmed + "│" + reset + cardRow("Memory", fmt.Sprintf("%d MB", memMB)) + dimmed + "│" + reset + "\n"

			// Row 3: Created | Disk
			created := time.Unix(0, alloc.CreateTime).Format("Jan 02 15:04:05")
			diskMB := 0
			if alloc.Resources != nil && alloc.Resources.DiskMB != nil {
				diskMB = *alloc.Resources.DiskMB
			}
			content += "  " + dimmed + "│" + reset + cardRow("Created", created) + dimmed + "│" + reset + gap
			content += dimmed + "│" + reset + cardRow("Disk", fmt.Sprintf("%d MB", diskMB)) + dimmed + "│" + reset + "\n"

			// Bottom border
			content += "  " + dimmed + "╰" + safeRepeat("─", cardWidth) + "╯" + reset + gap
			content += dimmed + "╰" + safeRepeat("─", cardWidth) + "╯" + reset + "\n\n"

			// Task States
			if len(alloc.TaskStates) > 0 {
				content += "  " + bold + cyan + "TASK STATES" + reset + "\n"

				// Calculate dynamic column widths for tasks table
				taskTableWidth := getTableWidth(m.width, 4)
				taskColWidths := calculateColumnWidths(taskTableWidth, []int{2, 2, 2, 3}, []int{12, 10, 12, 20})
				colTaskName := taskColWidths[0]
				colState := taskColWidths[1]
				colStarted := taskColWidths[2]
				colLastEvent := taskColWidths[3]

				content += "  " + dimmed + "╭" + safeRepeat("─", colTaskName) + "┬" + safeRepeat("─", colState) + "┬" + safeRepeat("─", colStarted) + "┬" + safeRepeat("─", colLastEvent) + "╮" + reset + "\n"
				content += "  " + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colTaskName-1, "Task") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colState-1, "State") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colStarted-1, "Started") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colLastEvent-1, "Last Event") + reset + dimmed + "│" + reset + "\n"
				content += "  " + dimmed + "├" + safeRepeat("─", colTaskName) + "┼" + safeRepeat("─", colState) + "┼" + safeRepeat("─", colStarted) + "┼" + safeRepeat("─", colLastEvent) + "┤" + reset + "\n"

				for taskName, taskState := range alloc.TaskStates {
					state := taskState.State
					stateColor := ""
					switch state {
					case "running":
						stateColor = m.theme.Running
					case "pending":
						stateColor = m.theme.Pending
					case "dead", "failed":
						stateColor = m.theme.Dead
					}

					started := "N/A"
					if !taskState.StartedAt.IsZero() {
						started = taskState.StartedAt.Format("15:04:05")
					}

					lastEvent := ""
					if len(taskState.Events) > 0 {
						lastEvent = taskState.Events[len(taskState.Events)-1].Type
					}

					nameField := fmt.Sprintf(" %-*s", colTaskName-1, truncate(taskName, colTaskName-2))
					stateField := fmt.Sprintf(" %-*s", colState-1, state)
					startedField := fmt.Sprintf(" %-*s", colStarted-1, started)
					eventField := fmt.Sprintf(" %-*s", colLastEvent-1, truncate(lastEvent, colLastEvent-2))

					content += "  " + dimmed + "│" + reset + nameField + dimmed + "│" + reset + stateColor + stateField + reset + dimmed + "│" + reset + startedField + dimmed + "│" + reset + eventField + dimmed + "│" + reset + "\n"
				}

				content += "  " + dimmed + "╰" + safeRepeat("─", colTaskName) + "┴" + safeRepeat("─", colState) + "┴" + safeRepeat("─", colStarted) + "┴" + safeRepeat("─", colLastEvent) + "╯" + reset + "\n"
			}
		}

		// Navigation hint
		content += "\n  " + cyan + "e" + reset + " Events  " + dimmed + "│" + reset + "  " + cyan + "l" + reset + " Logs  " + dimmed + "│" + reset + "  " + cyan + "Esc" + reset + " Back\n"

	case "alloc-events":
		// Header
		content = "\n" + renderHeader("📋 ALLOCATION EVENTS", boxWidth, m.theme.Header, bold, reset) + "\n"

		// Alloc info
		if m.selectedAlloc != nil {
			allocID := m.selectedAlloc.ID
			if len(allocID) > 16 {
				allocID = allocID[:16] + "..."
			}
			content += "  " + bold + allocID + reset + "\n"
			content += "  " + dimmed + "Job: " + m.selectedAlloc.JobID + " · Task Group: " + m.selectedAlloc.TaskGroup + reset + "\n\n"
		}

		// Events section
		content += "  " + bold + cyan + "RECENT EVENTS" + reset + "\n"

		if len(m.eventsList) == 0 {
			content += "  " + dimmed + "Loading events..." + reset + "\n"
		} else {
			// Calculate dynamic column widths for events table
			eventsTableWidth := getTableWidth(m.width, 5)
			eventsColWidths := calculateColumnWidths(eventsTableWidth, []int{3, 1, 2, 2, 4}, []int{16, 8, 10, 10, 16})
			colTime := eventsColWidths[0]
			colAlloc := eventsColWidths[1]
			colTask := eventsColWidths[2]
			colType := eventsColWidths[3]
			colMsg := eventsColWidths[4]

			content += "  " + dimmed + "╭" + safeRepeat("─", colTime) + "┬" + safeRepeat("─", colAlloc) + "┬" + safeRepeat("─", colTask) + "┬" + safeRepeat("─", colType) + "┬" + safeRepeat("─", colMsg) + "╮" + reset + "\n"
			content += "  " + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colTime-1, "Time") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colAlloc-1, "Alloc") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colTask-1, "Task") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colType-1, "Type") + reset + dimmed + "│" + reset + " " + bold + fmt.Sprintf("%-*s", colMsg-1, "Message") + reset + dimmed + "│" + reset + "\n"
			content += "  " + dimmed + "├" + safeRepeat("─", colTime) + "┼" + safeRepeat("─", colAlloc) + "┼" + safeRepeat("─", colTask) + "┼" + safeRepeat("─", colType) + "┼" + safeRepeat("─", colMsg) + "┤" + reset + "\n"

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

			content += "  " + dimmed + "╰" + safeRepeat("─", colTime) + "┴" + safeRepeat("─", colAlloc) + "┴" + safeRepeat("─", colTask) + "┴" + safeRepeat("─", colType) + "┴" + safeRepeat("─", colMsg) + "╯" + reset + "\n"
		}

		// Navigation hint
		content += "\n  " + cyan + "r" + reset + " Refresh  " + dimmed + "│" + reset + "  " + cyan + "Esc" + reset + " Back\n"

	case "eval-detail":
		// Header
		content = "\n" + renderHeader("📊 EVALUATION DETAILS", boxWidth, m.theme.Header, bold, reset) + "\n"

		if m.selectedEval == nil {
			content += "  " + dimmed + "No evaluation selected" + reset + "\n"
		} else {
			eval := m.selectedEval

			// Evaluation ID and status
			evalID := eval.ID
			if len(evalID) > 16 {
				evalID = evalID[:16] + "..."
			}
			statusLabel := strings.ToUpper(eval.Status)
			statusColor := ""
			switch eval.Status {
			case "complete":
				statusColor = m.theme.Running
			case "pending":
				statusColor = m.theme.Pending
			case "blocked", "failed", "canceled":
				statusColor = m.theme.Dead
			}

			content += "  " + bold + white + evalID + reset + "  "
			content += statusColor + "● " + statusLabel + reset + "\n"
			content += "  " + dimmed + "Job: " + eval.JobID + reset + "\n\n"

			// Info cards
			cardWidth := 38
			gap := "  "

			cardRow := func(label string, value string) string {
				labelPadded := fmt.Sprintf("%-14s", label)
				valuePadded := fmt.Sprintf("%-*s", cardWidth-17, truncate(value, cardWidth-17))
				return "  " + dimmed + labelPadded + reset + " " + valuePadded
			}

			// Top border
			content += "  " + dimmed + "╭" + safeRepeat("─", cardWidth) + "╮" + reset + gap
			content += dimmed + "╭" + safeRepeat("─", cardWidth) + "╮" + reset + "\n"

			// Card headers
			content += "  " + dimmed + "│" + reset + " " + bold + cyan + "EVALUATION INFO" + reset + safeRepeat(" ", cardWidth-16) + dimmed + "│" + reset + gap
			content += dimmed + "│" + reset + " " + bold + cyan + "PLACEMENT" + reset + safeRepeat(" ", cardWidth-10) + dimmed + "│" + reset + "\n"

			// Separator
			content += "  " + dimmed + "├" + safeRepeat("─", cardWidth) + "┤" + reset + gap
			content += dimmed + "├" + safeRepeat("─", cardWidth) + "┤" + reset + "\n"

			// Row 1: Triggered By | Status
			placementStatus := "OK"
			if eval.FailedTGAllocs != nil && len(eval.FailedTGAllocs) > 0 {
				failedCount := 0
				for _, metric := range eval.FailedTGAllocs {
					failedCount += metric.CoalescedFailures + 1
				}
				placementStatus = fmt.Sprintf("Failed (%d)", failedCount)
			} else if eval.Status == "blocked" {
				placementStatus = "Blocked"
			}
			content += "  " + dimmed + "│" + reset + cardRow("Triggered By", eval.TriggeredBy) + dimmed + "│" + reset + gap
			content += dimmed + "│" + reset + cardRow("Status", placementStatus) + dimmed + "│" + reset + "\n"

			// Row 2: Priority | Type
			content += "  " + dimmed + "│" + reset + cardRow("Priority", fmt.Sprintf("%d", eval.Priority)) + dimmed + "│" + reset + gap
			content += dimmed + "│" + reset + cardRow("Type", eval.Type) + dimmed + "│" + reset + "\n"

			// Row 3: Created | Wait Until
			created := time.Unix(0, eval.CreateTime).Format("Jan 02 15:04:05")
			waitUntil := "N/A"
			if eval.WaitUntil.IsZero() == false {
				waitUntil = eval.WaitUntil.Format("15:04:05")
			}
			content += "  " + dimmed + "│" + reset + cardRow("Created", created) + dimmed + "│" + reset + gap
			content += dimmed + "│" + reset + cardRow("Wait Until", waitUntil) + dimmed + "│" + reset + "\n"

			// Bottom border
			content += "  " + dimmed + "╰" + safeRepeat("─", cardWidth) + "╯" + reset + gap
			content += dimmed + "╰" + safeRepeat("─", cardWidth) + "╯" + reset + "\n\n"

			// Placement Failures
			if eval.FailedTGAllocs != nil && len(eval.FailedTGAllocs) > 0 {
				content += "  " + bold + m.theme.Dead + "⚠ PLACEMENT FAILURES" + reset + "\n"

				for taskGroup, metrics := range eval.FailedTGAllocs {
					content += "  " + dimmed + "╭" + safeRepeat("─", 76) + "╮" + reset + "\n"
					content += "  " + dimmed + "│" + reset + " " + bold + "Task Group: " + reset + white + taskGroup + reset + safeRepeat(" ", 76-14-len(taskGroup)) + dimmed + "│" + reset + "\n"
					content += "  " + dimmed + "├" + safeRepeat("─", 76) + "┤" + reset + "\n"

					evalInfo := fmt.Sprintf("Nodes Evaluated: %d  |  Nodes Filtered: %d  |  Nodes Exhausted: %d",
						metrics.NodesEvaluated, metrics.NodesFiltered, metrics.NodesExhausted)
					content += "  " + dimmed + "│" + reset + " " + fmt.Sprintf("%-75s", evalInfo) + dimmed + "│" + reset + "\n"

					if len(metrics.ConstraintFiltered) > 0 {
						content += "  " + dimmed + "│" + reset + " " + m.theme.Dead + "Constraint Failures:" + reset + safeRepeat(" ", 54) + dimmed + "│" + reset + "\n"
						for constraint, count := range metrics.ConstraintFiltered {
							constraintLine := fmt.Sprintf("  • %s (%d nodes)", truncate(constraint, 60), count)
							content += "  " + dimmed + "│" + reset + " " + fmt.Sprintf("%-75s", constraintLine) + dimmed + "│" + reset + "\n"
						}
					}

					if metrics.CoalescedFailures > 0 {
						coalescedLine := fmt.Sprintf("(+%d additional failures)", metrics.CoalescedFailures)
						content += "  " + dimmed + "│" + reset + " " + dimmed + fmt.Sprintf("%-75s", coalescedLine) + reset + dimmed + "│" + reset + "\n"
					}

					content += "  " + dimmed + "╰" + safeRepeat("─", 76) + "╯" + reset + "\n"
				}
			}
		}

		// Navigation hint
		content += "\n  " + cyan + "Esc" + reset + " Back\n"

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
	footerLine := "\033[48;5;237m" + "\033[37m" + safeRepeat(" ", leftPad) + "Press '\033[38;5;51mh\033[37m' for help, '\033[38;5;204mq\033[37m' for quit" + safeRepeat(" ", rightPad) + "\033[0m"
	content += "\n" + safeRepeat("─", m.width) + "\n" + footerLine + "\n"
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
		} else if len(m.confirmJobs) > 0 {
			// Job action confirmation
			action := strings.Title(m.confirmAction)
			if len(m.confirmJobs) == 1 {
				message = fmt.Sprintf("Are you sure you want to %s job '%s'?", action, m.confirmJobs[0].Name)
			} else {
				message = fmt.Sprintf("Are you sure you want to %s %d jobs?", action, len(m.confirmJobs))
			}
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
		padding := safeRepeat(" ", leftPad)

		// Build dialog box
		topBorder := padding + "╭" + safeRepeat("─", dialogWidth-2) + "╮"
		bottomBorder := padding + "╰" + safeRepeat("─", dialogWidth-2) + "╯"
		emptyLine := padding + "│" + safeRepeat(" ", dialogWidth-2) + "│"

		// Center the message
		msgPadLeft := (dialogWidth - 2 - len(message)) / 2
		msgPadRight := dialogWidth - 2 - len(message) - msgPadLeft
		messageLine := padding + "│" + safeRepeat(" ", msgPadLeft) + bold + message + reset + safeRepeat(" ", msgPadRight) + "│"

		// Options line
		options := "[" + m.theme.Running + "y" + reset + "]es  /  [" + m.theme.Dead + "n" + reset + "]o"
		optVisualLen := 12 // "y" + "es  /  " + "n" + "o" + brackets
		optPadLeft := (dialogWidth - 2 - optVisualLen) / 2
		optPadRight := dialogWidth - 2 - optVisualLen - optPadLeft
		optionsLine := padding + "│" + safeRepeat(" ", optPadLeft) + options + safeRepeat(" ", optPadRight) + "│"

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

	// Show filter overlay if active
	if m.filterActive {
		filterOverlay := renderFilterOverlay(m.width, m.height, m.filterInput, m.theme)

		// Split main content and overlay into lines
		contentLines := strings.Split(mainContent, "\n")
		overlayLines := strings.Split(filterOverlay, "\n")

		// Ensure content has enough lines
		for len(contentLines) < m.height {
			contentLines = append(contentLines, "")
		}

		// Overlay filter box onto content
		for i, oLine := range overlayLines {
			if oLine != "" && i < len(contentLines) {
				contentLines[i] = oLine
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
