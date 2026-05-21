package components

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/devnullvoid/pvetui/internal/ui/models"
	"github.com/devnullvoid/pvetui/internal/ui/theme"
	"github.com/devnullvoid/pvetui/internal/ui/utils"
	"github.com/devnullvoid/pvetui/pkg/api"
)

const (
	dashboardBarWidth           = 10
	dashboardSparklineMaxPoints = 30
)

type nomadSnapshot struct {
	updatedAt time.Time
	leader    string
	jobsTotal int
	jobsRun   int
	lastErr   string
}

// Dashboard is the Home page: a read-only, live telemetry view of the cluster.
type Dashboard struct {
	*tview.Flex

	app *App

	summary     *tview.TextView
	nodeTable   *tview.Table
	guestTable  *tview.Table
	taskTable   *tview.Table
	nomadStatus *tview.TextView

	mu          sync.Mutex
	cpuHistory  []float64
	memHistory  []float64
	taskHistory []float64
	nomad       nomadSnapshot
}

var _ DashboardComponent = (*Dashboard)(nil)

// NewDashboard creates the Home dashboard view.
func NewDashboard() *Dashboard {
	summary := tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(false)
	summary.SetBorder(true).
		SetTitle(" Home / Telemetry ").
		SetTitleColor(theme.Colors.Primary).
		SetBorderColor(theme.Colors.Border)

	nodeTable := tview.NewTable().
		SetSelectable(false, false)
	nodeTable.SetBorder(true).
		SetTitle(" Nodes ").
		SetTitleColor(theme.Colors.Primary).
		SetBorderColor(theme.Colors.Border)

	guestTable := tview.NewTable().
		SetSelectable(false, false)
	guestTable.SetBorder(true).
		SetTitle(" Guests ").
		SetTitleColor(theme.Colors.Primary).
		SetBorderColor(theme.Colors.Border)

	taskTable := tview.NewTable().
		SetSelectable(false, false)
	taskTable.SetBorder(true).
		SetTitle(" Activity ").
		SetTitleColor(theme.Colors.Primary).
		SetBorderColor(theme.Colors.Border)

	nomadStatus := tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(true)
	nomadStatus.SetBorder(true).
		SetTitle(" Nomad ").
		SetTitleColor(theme.Colors.Primary).
		SetBorderColor(theme.Colors.Border)
	nomadStatus.SetText(theme.ReplaceSemanticTags("[secondary]Nomad integration disabled (set `homepage.nomad_addr` in config)[-]"))

	rightCol := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(summary, 7, 0, false).
		AddItem(taskTable, 0, 1, false).
		AddItem(nomadStatus, 7, 0, false)

	main := tview.NewFlex().
		SetDirection(tview.FlexColumn).
		AddItem(nodeTable, 0, 2, false).
		AddItem(guestTable, 0, 2, false).
		AddItem(rightCol, 0, 1, false)

	return &Dashboard{
		Flex:        main,
		summary:     summary,
		nodeTable:   nodeTable,
		guestTable:  guestTable,
		taskTable:   taskTable,
		nomadStatus: nomadStatus,
	}
}

// SetApp sets the parent app reference and starts background tickers.
func (d *Dashboard) SetApp(app *App) {
	d.app = app
	if d.app == nil {
		return
	}

	d.startClockTicker(d.app.ctx)
	d.maybeStartNomadPolling(d.app.ctx)
}

// Refresh rebuilds all dashboard panels from current global state.
// Must be called from the tview UI goroutine.
func (d *Dashboard) Refresh() {
	if d.app == nil {
		return
	}

	cluster := d.app.getDisplayCluster()
	nodes := models.GlobalState.OriginalNodes
	vms := models.GlobalState.OriginalVMs
	tasks := models.GlobalState.OriginalTasks

	d.renderSummary(cluster, vms, tasks)
	d.renderNodes(nodes)
	d.renderGuests(vms)
	d.renderTasks(tasks)
	d.renderNomad()
}

func (d *Dashboard) startClockTicker(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if d.app == nil {
					continue
				}
				d.app.QueueUpdateDraw(func() {
					d.renderSummary(d.app.getDisplayCluster(), models.GlobalState.OriginalVMs, models.GlobalState.OriginalTasks)
				})
			}
		}
	}()
}

func (d *Dashboard) maybeStartNomadPolling(ctx context.Context) {
	addr := strings.TrimSpace(d.app.config.Homepage.NomadAddr)
	if addr == "" {
		return
	}

	d.nomadStatus.SetText(theme.ReplaceSemanticTags("[info]Nomad enabled[-]\n[secondary]Connecting…[-]"))

	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		// Do an immediate fetch.
		d.fetchNomadSnapshot(ctx, addr)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				d.fetchNomadSnapshot(ctx, addr)
			}
		}
	}()
}

func (d *Dashboard) fetchNomadSnapshot(parent context.Context, addr string) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()

	httpClient := &http.Client{Timeout: 2 * time.Second}

	var snap nomadSnapshot
	snap.updatedAt = time.Now()

	leader, err := nomadGetLeader(ctx, httpClient, addr)
	if err != nil {
		snap.lastErr = err.Error()
		d.setNomadSnapshot(snap)
		return
	}
	snap.leader = leader

	jobsTotal, jobsRun, err := nomadGetJobCounts(ctx, httpClient, addr)
	if err != nil {
		snap.lastErr = err.Error()
		d.setNomadSnapshot(snap)
		return
	}
	snap.jobsTotal = jobsTotal
	snap.jobsRun = jobsRun

	d.setNomadSnapshot(snap)
}

func (d *Dashboard) setNomadSnapshot(snap nomadSnapshot) {
	d.mu.Lock()
	d.nomad = snap
	d.mu.Unlock()

	if d.app == nil {
		return
	}

	d.app.QueueUpdateDraw(func() {
		d.renderNomad()
	})
}

func (d *Dashboard) renderNomad() {
	if d.app == nil {
		return
	}

	addr := strings.TrimSpace(d.app.config.Homepage.NomadAddr)
	if addr == "" {
		return
	}

	d.mu.Lock()
	snap := d.nomad
	d.mu.Unlock()

	if snap.lastErr != "" {
		d.nomadStatus.SetText(theme.ReplaceSemanticTags(fmt.Sprintf(
			"[error]Unreachable[-]\n[secondary]%s[-]\n[secondary]Addr: %s[-]",
			snap.lastErr,
			addr,
		)))
		return
	}

	age := time.Since(snap.updatedAt).Round(time.Second)
	if age < 0 {
		age = 0
	}

	leader := snap.leader
	if leader == "" {
		leader = api.StringNA
	}

	d.nomadStatus.SetText(theme.ReplaceSemanticTags(fmt.Sprintf(
		"[success]Connected[-]\n[primary]Leader:[-] %s\n[primary]Jobs:[-] %d total, %d running\n[secondary]Updated %s ago[-]\n[secondary]Addr: %s[-]",
		leader,
		snap.jobsTotal,
		snap.jobsRun,
		age,
		addr,
	)))
}

func (d *Dashboard) renderSummary(cluster *api.Cluster, vms []*api.VM, tasks []*api.ClusterTask) {
	now := time.Now()

	showIcons := true
	autoRefresh := false
	autoRefreshCountdown := 0
	if d.app != nil {
		showIcons = d.app.config.ShowIcons
		autoRefresh = d.app.autoRefreshEnabled
		autoRefreshCountdown = d.app.autoRefreshCountdown
	}

	vmRunning, vmStopped, vmTemplates := summarizeGuests(vms)
	tasksActive := countActiveTasks(tasks)

	var clusterName, version, quorate string
	var nodeOnline, nodeTotal int
	var cpuPct, memPct, storagePct float64

	if cluster != nil {
		clusterName = cluster.Name
		version = cluster.Version
		quorate = fmt.Sprintf("%t", cluster.Quorate)
		nodeOnline = cluster.OnlineNodes
		nodeTotal = cluster.TotalNodes

		cpuPct = clampPercent(cluster.CPUUsage * 100)
		memPct = clampPercent(utils.CalculatePercentage(cluster.MemoryUsed, cluster.MemoryTotal))
		storagePct = clampPercent(utils.CalculatePercentageInt(cluster.StorageUsed, cluster.StorageTotal))
	}

	if version == "" {
		version = api.StringNA
	}
	if clusterName == "" {
		clusterName = api.StringNA
	}
	if nodeTotal == 0 {
		nodeTotal = len(models.GlobalState.OriginalNodes)
	}

	d.mu.Lock()
	d.cpuHistory = appendLimited(d.cpuHistory, cpuPct, dashboardSparklineMaxPoints)
	d.memHistory = appendLimited(d.memHistory, memPct, dashboardSparklineMaxPoints)
	d.taskHistory = appendLimited(d.taskHistory, float64(tasksActive), dashboardSparklineMaxPoints)
	cpuSpark := sparkline(d.cpuHistory)
	memSpark := sparkline(d.memHistory)
	taskSpark := sparklineScaled(d.taskHistory)
	d.mu.Unlock()

	quorateText := quorate
	if nodeTotal <= 1 {
		quorateText = api.StringNA
	}

	autoRefreshText := "OFF"
	if autoRefresh {
		if autoRefreshCountdown > 0 {
			autoRefreshText = fmt.Sprintf("ON (%ds)", autoRefreshCountdown)
		} else {
			autoRefreshText = "ON"
		}
	}

	nodeStatusIndicator := "✓"
	if showIcons {
		nodeStatusIndicator = "🟢"
	}
	if nodeTotal > 0 && nodeOnline < nodeTotal {
		nodeStatusIndicator = "!"
		if showIcons {
			nodeStatusIndicator = "⚠️"
		}
	}

	header := fmt.Sprintf("[primary]%s[-]  [secondary]%s[-]\n", clusterName, now.Format("2006-01-02 15:04:05"))
	line1 := fmt.Sprintf(
		"[primary]PVE:[-] %s  [primary]Nodes:[-] %d/%d %s  [primary]Quorate:[-] %s  [primary]Auto-Refresh:[-] %s\n",
		version,
		nodeOnline,
		nodeTotal,
		nodeStatusIndicator,
		quorateText,
		autoRefreshText,
	)
	line2 := fmt.Sprintf(
		"[primary]Guests:[-] %d running / %d stopped / %d templates  [primary]Tasks:[-] %d active\n",
		vmRunning,
		vmStopped,
		vmTemplates,
		tasksActive,
	)
	line3 := fmt.Sprintf(
		"[primary]CPU:[-] %5.1f%% %s  [primary]Mem:[-] %5.1f%% %s  [primary]Storage:[-] %5.1f%%\n",
		cpuPct,
		cpuSpark,
		memPct,
		memSpark,
		storagePct,
	)
	line4 := fmt.Sprintf("[primary]Pulse:[-] tasks %s\n", taskSpark)

	d.summary.SetText(theme.ReplaceSemanticTags(header + line1 + line2 + line3 + line4))
}

func (d *Dashboard) renderNodes(nodes []*api.Node) {
	d.nodeTable.Clear()

	headers := []string{"Node", "CPU", "Mem", "Disk", "Load"}
	for col, h := range headers {
		cell := tview.NewTableCell(h).
			SetTextColor(theme.Colors.HeaderText).
			SetAlign(tview.AlignLeft).
			SetSelectable(false)
		d.nodeTable.SetCell(0, col, cell)
	}

	showIcons := true
	if d.app != nil {
		showIcons = d.app.config.ShowIcons
	}

	// Keep ordering stable by name.
	sorted := append([]*api.Node(nil), nodes...)
	sort.SliceStable(sorted, func(i, j int) bool {
		ni, nj := sorted[i], sorted[j]
		if ni == nil {
			return false
		}
		if nj == nil {
			return true
		}
		return strings.ToLower(ni.Name) < strings.ToLower(nj.Name)
	})

	maxRows := 12
	row := 1
	for _, n := range sorted {
		if n == nil {
			continue
		}
		if row > maxRows {
			d.nodeTable.SetCell(row, 0, tview.NewTableCell("…").SetTextColor(theme.Colors.Secondary))
			break
		}

		stateIcon := "✓"
		stateColor := theme.Colors.StatusRunning
		if showIcons {
			stateIcon = "🟢"
		}
		if !n.Online {
			stateIcon = "✗"
			stateColor = theme.Colors.StatusStopped
			if showIcons {
				stateIcon = "🔴"
			}
		}

		nodeLabel := fmt.Sprintf("%s %s", stateIcon, n.Name)
		d.nodeTable.SetCell(row, 0, tview.NewTableCell(nodeLabel).SetTextColor(stateColor))

		cpuPct := clampPercent(n.CPUUsage * 100)
		cpuText := fmt.Sprintf("%5.1f%% %s", cpuPct, bar(cpuPct, dashboardBarWidth))
		d.nodeTable.SetCell(row, 1, tview.NewTableCell(cpuText).SetTextColor(theme.GetUsageColor(cpuPct)))

		memPct := clampPercent(utils.CalculatePercentage(n.MemoryUsed, n.MemoryTotal))
		memText := fmt.Sprintf("%5.1f%% %s", memPct, bar(memPct, dashboardBarWidth))
		d.nodeTable.SetCell(row, 2, tview.NewTableCell(memText).SetTextColor(theme.GetUsageColor(memPct)))

		diskPct := clampPercent(utils.CalculatePercentageInt(n.UsedStorage*1024*1024*1024, n.TotalStorage*1024*1024*1024))
		diskText := fmt.Sprintf("%5.1f%% %s", diskPct, bar(diskPct, dashboardBarWidth))
		d.nodeTable.SetCell(row, 3, tview.NewTableCell(diskText).SetTextColor(theme.GetUsageColor(diskPct)))

		load := api.StringNA
		if len(n.LoadAvg) > 0 {
			load = strings.Join(n.LoadAvg, " ")
		}
		d.nodeTable.SetCell(row, 4, tview.NewTableCell(load).SetTextColor(theme.Colors.Secondary))

		row++
	}
}

func (d *Dashboard) renderGuests(vms []*api.VM) {
	d.guestTable.Clear()

	d.guestTable.SetCell(0, 0, tview.NewTableCell("Top CPU").SetTextColor(theme.Colors.HeaderText))
	d.guestTable.SetCell(0, 1, tview.NewTableCell("Top Memory").SetTextColor(theme.Colors.HeaderText))

	type vmScore struct {
		vm       *api.VM
		cpuPct   float64
		memPct   float64
		memBytes int64
	}

	scored := make([]vmScore, 0, len(vms))
	for _, vm := range vms {
		if vm == nil {
			continue
		}
		cpuPct := clampPercent(vm.CPU * 100)
		memPct := clampPercent(utils.CalculatePercentage(float64(vm.Mem), float64(vm.MaxMem)))
		scored = append(scored, vmScore{vm: vm, cpuPct: cpuPct, memPct: memPct, memBytes: vm.Mem})
	}

	byCPU := append([]vmScore(nil), scored...)
	sort.SliceStable(byCPU, func(i, j int) bool { return byCPU[i].cpuPct > byCPU[j].cpuPct })
	byMem := append([]vmScore(nil), scored...)
	sort.SliceStable(byMem, func(i, j int) bool { return byMem[i].memPct > byMem[j].memPct })

	showIcons := true
	if d.app != nil {
		showIcons = d.app.config.ShowIcons
	}

	maxRows := 12
	for i := 0; i < maxRows; i++ {
		row := i + 1

		if i < len(byCPU) {
			cellText, color := formatVMCell(byCPU[i].vm, byCPU[i].cpuPct, showIcons, "cpu")
			d.guestTable.SetCell(row, 0, tview.NewTableCell(cellText).SetTextColor(color))
		}

		if i < len(byMem) {
			cellText, color := formatVMCell(byMem[i].vm, byMem[i].memPct, showIcons, "mem")
			d.guestTable.SetCell(row, 1, tview.NewTableCell(cellText).SetTextColor(color))
		}
	}
}

func (d *Dashboard) renderTasks(tasks []*api.ClusterTask) {
	d.taskTable.Clear()

	headers := []string{"State", "Type", "Node", "Age"}
	for col, h := range headers {
		d.taskTable.SetCell(0, col, tview.NewTableCell(h).SetTextColor(theme.Colors.HeaderText))
	}

	running := make([]*api.ClusterTask, 0, len(tasks))
	finished := make([]*api.ClusterTask, 0, len(tasks))
	for _, t := range tasks {
		if t == nil {
			continue
		}
		if t.EndTime > 0 {
			finished = append(finished, t)
		} else {
			running = append(running, t)
		}
	}

	sort.SliceStable(running, func(i, j int) bool { return running[i].StartTime > running[j].StartTime })
	sort.SliceStable(finished, func(i, j int) bool { return finished[i].EndTime > finished[j].EndTime })

	showIcons := true
	if d.app != nil {
		showIcons = d.app.config.ShowIcons
	}

	row := 1
	maxRows := 12

	for _, t := range running {
		if row > maxRows {
			break
		}
		stateText, stateColor := formatTaskState(t, showIcons)
		d.taskTable.SetCell(row, 0, tview.NewTableCell(stateText).SetTextColor(stateColor))
		d.taskTable.SetCell(row, 1, tview.NewTableCell(taskTypeShort(t.Type)).SetTextColor(theme.Colors.Primary))
		d.taskTable.SetCell(row, 2, tview.NewTableCell(t.Node).SetTextColor(theme.Colors.Secondary))
		d.taskTable.SetCell(row, 3, tview.NewTableCell(timeSinceUnix(t.StartTime)).SetTextColor(theme.Colors.Secondary))
		row++
	}

	// Spacer between running and finished, if room.
	if row < maxRows && len(running) > 0 && len(finished) > 0 {
		row++
	}

	for _, t := range finished {
		if row > maxRows {
			break
		}
		stateText, stateColor := formatTaskState(t, showIcons)
		d.taskTable.SetCell(row, 0, tview.NewTableCell(stateText).SetTextColor(stateColor))
		d.taskTable.SetCell(row, 1, tview.NewTableCell(taskTypeShort(t.Type)).SetTextColor(theme.Colors.Primary))
		d.taskTable.SetCell(row, 2, tview.NewTableCell(t.Node).SetTextColor(theme.Colors.Secondary))
		d.taskTable.SetCell(row, 3, tview.NewTableCell(timeSinceUnix(t.EndTime)).SetTextColor(theme.Colors.Secondary))
		row++
	}
}

func summarizeGuests(vms []*api.VM) (running, stopped, templates int) {
	for _, vm := range vms {
		if vm == nil {
			continue
		}
		if vm.Template {
			templates++
			continue
		}
		switch strings.ToLower(vm.Status) {
		case api.VMStatusRunning:
			running++
		case api.VMStatusStopped:
			stopped++
		default:
			// count unknown as stopped-ish for summary
			stopped++
		}
	}
	return running, stopped, templates
}

func countActiveTasks(tasks []*api.ClusterTask) int {
	count := 0
	for _, t := range tasks {
		if t == nil {
			continue
		}
		if t.EndTime == 0 {
			count++
		}
	}
	return count
}

func clampPercent(p float64) float64 {
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

func bar(percent float64, width int) string {
	if width <= 0 {
		return ""
	}
	fill := int((percent / 100.0) * float64(width))
	if fill < 0 {
		fill = 0
	}
	if fill > width {
		fill = width
	}
	return strings.Repeat("█", fill) + strings.Repeat("░", width-fill)
}

func appendLimited(hist []float64, v float64, max int) []float64 {
	if max <= 0 {
		return hist
	}
	hist = append(hist, v)
	if len(hist) <= max {
		return hist
	}
	return hist[len(hist)-max:]
}

func sparkline(hist []float64) string {
	if len(hist) == 0 {
		return ""
	}
	blocks := []rune("▁▂▃▄▅▆▇█")
	var b strings.Builder
	for _, v := range hist {
		idx := int((clampPercent(v) / 100.0) * float64(len(blocks)-1))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(blocks) {
			idx = len(blocks) - 1
		}
		b.WriteRune(blocks[idx])
	}
	return b.String()
}

func sparklineScaled(hist []float64) string {
	if len(hist) == 0 {
		return ""
	}
	max := 0.0
	for _, v := range hist {
		if v > max {
			max = v
		}
	}
	if max <= 0 {
		return sparkline(hist)
	}
	blocks := []rune("▁▂▃▄▅▆▇█")
	var b strings.Builder
	for _, v := range hist {
		ratio := v / max
		idx := int(ratio * float64(len(blocks)-1))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(blocks) {
			idx = len(blocks) - 1
		}
		b.WriteRune(blocks[idx])
	}
	return b.String()
}

func formatVMCell(vm *api.VM, pct float64, showIcons bool, metric string) (string, tcell.Color) {
	if vm == nil {
		return "", theme.Colors.Secondary
	}

	stateIcon := "●"
	stateColor := theme.Colors.Warning
	if vm.Template {
		stateIcon = "T"
		stateColor = theme.Colors.Info
	} else {
		switch strings.ToLower(vm.Status) {
		case api.VMStatusRunning:
			stateIcon = "✓"
			stateColor = theme.Colors.StatusRunning
			if showIcons {
				stateIcon = "🟢"
			}
		case api.VMStatusStopped:
			stateIcon = "✗"
			stateColor = theme.Colors.StatusStopped
			if showIcons {
				stateIcon = "🔴"
			}
		default:
			if showIcons {
				stateIcon = "🟡"
			}
		}
	}

	name := vm.Name
	if name == "" {
		name = fmt.Sprintf("%d", vm.ID)
	}
	label := fmt.Sprintf("%s %d %s", stateIcon, vm.ID, name)

	switch metric {
	case "cpu":
		return fmt.Sprintf("%s  %4.1f%%", label, pct), theme.GetUsageColor(pct)
	case "mem":
		return fmt.Sprintf("%s  %4.1f%%", label, pct), theme.GetUsageColor(pct)
	default:
		return fmt.Sprintf("%s  %4.1f%%", label, pct), stateColor
	}
}

func formatTaskState(t *api.ClusterTask, showIcons bool) (string, tcell.Color) {
	if t == nil {
		return "", theme.Colors.Secondary
	}

	isRunning := t.EndTime == 0
	status := strings.TrimSpace(strings.ToUpper(t.Status))
	if isRunning {
		if showIcons {
			return "⏳", theme.Colors.Warning
		}
		return "RUN", theme.Colors.Warning
	}

	if status == "" || status == "OK" {
		if showIcons {
			return "✓", theme.Colors.StatusRunning
		}
		return "OK", theme.Colors.StatusRunning
	}

	if showIcons {
		return "✗", theme.Colors.StatusStopped
	}
	return "ERR", theme.Colors.StatusStopped
}

func taskTypeShort(taskType string) string {
	taskType = strings.TrimSpace(taskType)
	if taskType == "" {
		return api.StringNA
	}
	// Keep this short for narrow terminals; show last path segment if present.
	if idx := strings.LastIndex(taskType, "/"); idx >= 0 && idx < len(taskType)-1 {
		return taskType[idx+1:]
	}
	return taskType
}

func timeSinceUnix(ts int64) string {
	if ts <= 0 {
		return api.StringNA
	}
	d := time.Since(time.Unix(ts, 0))
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

type nomadLeaderResponse string

func nomadGetLeader(ctx context.Context, httpClient *http.Client, baseURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/v1/status/leader", nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	res, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request leader: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", fmt.Errorf("leader status %d", res.StatusCode)
	}

	var leader nomadLeaderResponse
	if err := json.NewDecoder(res.Body).Decode(&leader); err != nil {
		return "", fmt.Errorf("decode leader: %w", err)
	}

	return strings.Trim(string(leader), "\""), nil
}

type nomadJobListStub struct {
	Name   string `json:"Name"`
	Status string `json:"Status"`
}

func nomadGetJobCounts(ctx context.Context, httpClient *http.Client, baseURL string) (total int, running int, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/v1/jobs", nil)
	if err != nil {
		return 0, 0, fmt.Errorf("create request: %w", err)
	}

	res, err := httpClient.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("request jobs: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return 0, 0, fmt.Errorf("jobs status %d", res.StatusCode)
	}

	var jobs []nomadJobListStub
	if err := json.NewDecoder(res.Body).Decode(&jobs); err != nil {
		return 0, 0, fmt.Errorf("decode jobs: %w", err)
	}

	for _, job := range jobs {
		total++
		if strings.EqualFold(strings.TrimSpace(job.Status), "running") {
			running++
		}
	}

	return total, running, nil
}
