package components

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/devnullvoid/pvetui/internal/ui/models"
	"github.com/devnullvoid/pvetui/internal/ui/theme"
	"github.com/devnullvoid/pvetui/internal/ui/utils"
	"github.com/devnullvoid/pvetui/pkg/api"
)

const (
	maxTrendPoints = 24
)

// HomeDashboard is a read-only cluster command center page.
type HomeDashboard struct {
	*tview.Flex

	app *App

	topBand       *tview.TextView
	nodeHeatmap   *tview.TextView
	guestActivity *tview.TextView
	eventStream   *tview.TextView
	serviceFabric *tview.TextView
	upperRow      *tview.Flex
	lowerRow      *tview.Flex

	showNodeHeatmap bool
	showGuestPanel  bool
	showEventPanel  bool
	showServicePane bool
	compactMode     bool
	largeMode       bool
	pulseOn         bool

	cpuTrend     []float64
	memoryTrend  []float64
	storageTrend []float64

	lastIncidentAt  time.Time
	lastBackupOKAt  time.Time
	backgroundStart bool
}

// NewHomeDashboard creates the Home dashboard page.
func NewHomeDashboard() *HomeDashboard {
	hd := &HomeDashboard{
		Flex:            tview.NewFlex().SetDirection(tview.FlexRow),
		showNodeHeatmap: true,
		showGuestPanel:  true,
		showEventPanel:  true,
		showServicePane: true,
	}

	hd.topBand = hd.newPanel(" Home Command Center ")
	hd.nodeHeatmap = hd.newPanel(" Node Heatmap [1:toggle] ")
	hd.guestActivity = hd.newPanel(" Guest Activity [2:toggle] ")
	hd.eventStream = hd.newPanel(" Event Stream [3:toggle] ")
	hd.serviceFabric = hd.newPanel(" Service Fabric [4:toggle] ")

	hd.upperRow = tview.NewFlex().SetDirection(tview.FlexColumn)
	hd.lowerRow = tview.NewFlex().SetDirection(tview.FlexColumn)

	hd.AddItem(hd.topBand, 5, 0, true)
	hd.AddItem(hd.upperRow, 0, 1, false)
	hd.AddItem(hd.lowerRow, 0, 1, false)
	hd.syncRows()
	hd.installInputCapture()

	return hd
}

func (hd *HomeDashboard) newPanel(title string) *tview.TextView {
	tv := tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(false)
	tv.SetBorder(true)
	tv.SetTitle(title)
	tv.SetBorderColor(theme.Colors.Border)
	return tv
}

func (hd *HomeDashboard) installInputCapture() {
	hd.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if hd.app == nil {
			return event
		}
		if event.Key() != tcell.KeyRune {
			return event
		}
		switch strings.ToLower(string(event.Rune())) {
		case "n":
			hd.app.pages.SwitchToPage(api.PageNodes)
			hd.app.SetFocus(hd.app.nodeList)
			return nil
		case "g":
			hd.app.pages.SwitchToPage(api.PageGuests)
			hd.app.SetFocus(hd.app.vmList)
			return nil
		case "t":
			hd.app.pages.SwitchToPage(api.PageTasks)
			hd.app.SetFocus(hd.app.tasksList)
			return nil
		case "1":
			hd.showNodeHeatmap = !hd.showNodeHeatmap
		case "2":
			hd.showGuestPanel = !hd.showGuestPanel
		case "3":
			hd.showEventPanel = !hd.showEventPanel
		case "4":
			hd.showServicePane = !hd.showServicePane
		case "0":
			hd.showNodeHeatmap = true
			hd.showGuestPanel = true
			hd.showEventPanel = true
			hd.showServicePane = true
		case "c":
			hd.compactMode = !hd.compactMode
		case "l":
			hd.largeMode = !hd.largeMode
		default:
			return event
		}

		hd.syncRows()
		hd.renderFromState()
		return nil
	})
}

// SetApp links dashboard updates to the application lifecycle.
func (hd *HomeDashboard) SetApp(app *App) {
	hd.app = app
	if hd.app == nil || hd.backgroundStart {
		return
	}
	hd.backgroundStart = true

	hd.app.QueueUpdateDraw(func() {
		hd.renderFromState()
	})

	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-hd.app.ctx.Done():
				return
			case <-ticker.C:
				hd.app.QueueUpdateDraw(func() {
					hd.renderFromState()
				})
			}
		}
	}()
}

func (hd *HomeDashboard) syncRows() {
	hd.upperRow.Clear()
	hd.lowerRow.Clear()

	if hd.showNodeHeatmap {
		hd.upperRow.AddItem(hd.nodeHeatmap, 0, 1, false)
	}
	if hd.showGuestPanel {
		hd.upperRow.AddItem(hd.guestActivity, 0, 1, false)
	}
	if hd.upperRow.GetItemCount() == 0 {
		hd.upperRow.AddItem(hd.placeholder("Upper panels hidden"), 0, 1, false)
	}

	if hd.showEventPanel {
		hd.lowerRow.AddItem(hd.eventStream, 0, 1, false)
	}
	if hd.showServicePane {
		hd.lowerRow.AddItem(hd.serviceFabric, 0, 1, false)
	}
	if hd.lowerRow.GetItemCount() == 0 {
		hd.lowerRow.AddItem(hd.placeholder("Lower panels hidden"), 0, 1, false)
	}
}

func (hd *HomeDashboard) placeholder(text string) *tview.TextView {
	tv := tview.NewTextView().SetDynamicColors(true)
	tv.SetBorder(true)
	tv.SetTitle(" Hidden ")
	tv.SetText(theme.ReplaceSemanticTags(fmt.Sprintf("[secondary]%s[-]", text)))
	return tv
}

func (hd *HomeDashboard) renderFromState() {
	if hd.app == nil {
		return
	}
	cluster := hd.app.getDisplayCluster()
	if cluster == nil {
		hd.topBand.SetText(theme.ReplaceSemanticTags("[warning]No cluster data loaded yet[-]"))
		return
	}

	vms := append([]*api.VM(nil), models.GlobalState.OriginalVMs...)
	tasks := append([]*api.ClusterTask(nil), models.GlobalState.OriginalTasks...)
	activeOps := 0
	if tm := hd.app.TaskManager(); tm != nil {
		activeOps = len(tm.GetAllTasks())
	}

	hd.pulseOn = !hd.pulseOn
	hd.pushTrend(cluster.CPUUsage*100, utils.CalculatePercentage(cluster.MemoryUsed, cluster.MemoryTotal), utils.CalculatePercentageInt(cluster.StorageUsed, cluster.StorageTotal))

	health, incidents := hd.calculateHealth(cluster, tasks, activeOps)
	if incidents == 0 {
		if hd.lastIncidentAt.IsZero() {
			hd.lastIncidentAt = time.Now()
		}
	} else {
		hd.lastIncidentAt = time.Now()
	}
	hd.lastBackupOKAt = findLastBackupSuccess(tasks, hd.lastBackupOKAt)

	hd.topBand.SetText(theme.ReplaceSemanticTags(hd.buildTopBandText(cluster, health, incidents, activeOps)))
	hd.nodeHeatmap.SetText(theme.ReplaceSemanticTags(hd.buildNodeHeatmapText(cluster)))
	hd.guestActivity.SetText(theme.ReplaceSemanticTags(hd.buildGuestActivityText(vms, tasks)))
	hd.eventStream.SetText(theme.ReplaceSemanticTags(hd.buildEventStreamText(tasks)))
	hd.serviceFabric.SetText(theme.ReplaceSemanticTags(hd.buildServiceFabricText(vms)))
}

func (hd *HomeDashboard) buildTopBandText(cluster *api.Cluster, health int, incidents int, activeOps int) string {
	quorum := "[success]YES[-]"
	if cluster.TotalNodes > 1 && !cluster.Quorate {
		quorum = "[error]NO[-]"
	}
	pulse := "[secondary]○[-]"
	if activeOps > 0 {
		if hd.pulseOn {
			pulse = "[warning]◉[-]"
		} else {
			pulse = "[warning]◎[-]"
		}
	}

	riskTag := "[success]NORMAL[-]"
	if health < 70 {
		riskTag = "[warning]ELEVATED[-]"
	}
	if health < 45 {
		riskTag = "[error]CRITICAL[-]"
	}

	lastIncident := "N/A"
	if !hd.lastIncidentAt.IsZero() {
		lastIncident = formatSince(hd.lastIncidentAt)
	}
	lastBackup := "N/A"
	if !hd.lastBackupOKAt.IsZero() {
		lastBackup = formatSince(hd.lastBackupOKAt)
	}

	title := "[primary]HOME COMMAND CENTER[-]"
	if hd.largeMode {
		title = "[primary]HOME COMMAND CENTER - LARGE MODE[-]"
	}

	return fmt.Sprintf(
		"%s\n[info]Health[-]: [primary]%d[-]  [info]Risk[-]: %s  [info]Nodes[-]: [primary]%d/%d[-]  [info]Quorum[-]: %s  [info]Active Ops[-]: %s [primary]%d[-]  [info]Incidents[-]: [primary]%d[-]\n[info]CPU[-] %s  [info]MEM[-] %s  [info]STO[-] %s\n[info]Last Incident[-]: [primary]%s[-]  [info]Last Successful Backup[-]: [primary]%s[-]  [secondary]Shortcuts: n=Nodes g=Guests t=Tasks c=Compact l=Large 0=Reset[-]",
		title,
		health,
		riskTag,
		cluster.OnlineNodes, max(1, cluster.TotalNodes),
		quorum,
		pulse, activeOps,
		incidents,
		sparkline(hd.cpuTrend), sparkline(hd.memoryTrend), sparkline(hd.storageTrend),
		lastIncident, lastBackup,
	)
}

func (hd *HomeDashboard) buildNodeHeatmapText(cluster *api.Cluster) string {
	nodes := append([]*api.Node(nil), cluster.Nodes...)
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i] == nil || nodes[j] == nil {
			return nodes[i] != nil
		}
		return nodes[i].Name < nodes[j].Name
	})

	var b strings.Builder
	fmt.Fprintf(&b, "[info]Node utilization heatmap (CPU/MEM/STO)[-]\n")
	maxLines := 7
	if hd.compactMode {
		maxLines = 4
	}
	for _, node := range nodes {
		if node == nil {
			continue
		}
		if maxLines <= 0 {
			break
		}
		memPct := utils.CalculatePercentage(node.MemoryUsed, node.MemoryTotal)
		stoPct := utils.CalculatePercentageInt(node.UsedStorage, node.TotalStorage)
		cpuPct := node.CPUUsage * 100
		status := "[success]ONLINE[-]"
		if !node.Online {
			status = "[error]OFFLINE[-]"
		}
		fmt.Fprintf(&b, "%-14s %s  CPU:%s %5.1f%%[-] MEM:%s %5.1f%%[-] STO:%s %5.1f%%[-]\n",
			truncate(node.Name, 14),
			status,
			usageTag(cpuPct), cpuPct,
			usageTag(memPct), memPct,
			usageTag(stoPct), stoPct,
		)
		maxLines--
	}
	if len(nodes) == 0 {
		b.WriteString("[secondary]No nodes available[-]\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (hd *HomeDashboard) buildGuestActivityText(vms []*api.VM, tasks []*api.ClusterTask) string {
	running := 0
	stopped := 0
	templates := 0
	for _, vm := range vms {
		if vm == nil {
			continue
		}
		if vm.Template {
			templates++
			continue
		}
		if vm.Status == api.VMStatusRunning {
			running++
		} else {
			stopped++
		}
	}

	topCPU := topGuests(vms, func(vm *api.VM) float64 { return vm.CPU * 100 })
	topMem := topGuests(vms, func(vm *api.VM) float64 {
		if vm.MaxMem <= 0 {
			return 0
		}
		return utils.CalculatePercentageInt(vm.Mem, vm.MaxMem)
	})

	recentLifecycle := recentTaskSummary(tasks, []string{"start", "stop", "shutdown", "reboot", "reset"}, 3)
	var b strings.Builder
	fmt.Fprintf(&b, "[info]Guests[-]: [primary]%d[-]  [info]Running[-]: [success]%d[-]  [info]Stopped[-]: [warning]%d[-]  [info]Templates[-]: [secondary]%d[-]\n", len(vms), running, stopped, templates)
	fmt.Fprintf(&b, "[info]Top CPU[-]: %s\n", strings.Join(topCPU, "  "))
	fmt.Fprintf(&b, "[info]Top MEM[-]: %s\n", strings.Join(topMem, "  "))
	fmt.Fprintf(&b, "[info]Recent lifecycle events[-]:\n%s", strings.Join(recentLifecycle, "\n"))
	return strings.TrimRight(b.String(), "\n")
}

func (hd *HomeDashboard) buildEventStreamText(tasks []*api.ClusterTask) string {
	if len(tasks) == 0 {
		return "[secondary]No task history yet[-]"
	}
	sorted := append([]*api.ClusterTask(nil), tasks...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].StartTime > sorted[j].StartTime
	})
	maxLines := 8
	if hd.compactMode {
		maxLines = 5
	}
	var b strings.Builder
	for _, task := range sorted {
		if task == nil || maxLines == 0 {
			continue
		}
		icon, color := taskStatusDecor(task.Status, task.EndTime)
		when := "now"
		if task.StartTime > 0 {
			when = time.Unix(task.StartTime, 0).Format("15:04:05")
		}
		src := task.Node
		if task.SourceProfile != "" {
			src = fmt.Sprintf("%s/%s", task.SourceProfile, task.Node)
		}
		fmt.Fprintf(&b, "%s [%s]%s[-] [secondary]%s[-] [info]%s[-] [primary]%s[-]\n",
			icon,
			color,
			truncate(task.Type, 16),
			truncate(src, 18),
			when,
			truncate(task.Status, 14),
		)
		maxLines--
	}
	return strings.TrimRight(b.String(), "\n")
}

func (hd *HomeDashboard) buildServiceFabricText(vms []*api.VM) string {
	services := []struct {
		label string
		keys  []string
	}{
		{label: "Consul", keys: []string{"consul"}},
		{label: "Nomad Server", keys: []string{"nomad-server"}},
		{label: "Nomad Client", keys: []string{"nomad-client"}},
		{label: "Traefik", keys: []string{"traefik"}},
		{label: "Cloudflared", keys: []string{"cloudflared"}},
		{label: "Authentik", keys: []string{"authentik"}},
		{label: "Dokploy", keys: []string{"dokploy"}},
		{label: "PBS", keys: []string{"proxmox-backup-server", "pbs"}},
	}

	serviceState := map[string]string{}
	serviceNode := map[string]string{}
	for _, vm := range vms {
		if vm == nil {
			continue
		}
		nameLower := strings.ToLower(vm.Name)
		for _, service := range services {
			for _, key := range service.keys {
				if strings.Contains(nameLower, key) {
					if vm.Status == api.VMStatusRunning {
						serviceState[service.label] = "[success]ONLINE[-]"
					} else if _, exists := serviceState[service.label]; !exists {
						serviceState[service.label] = "[warning]DEGRADED[-]"
					}
					serviceNode[service.label] = vm.Node
					break
				}
			}
		}
	}

	var b strings.Builder
	b.WriteString("[info]Service fabric status[-]\n")
	nomadPresent := false
	for _, service := range services {
		state := serviceState[service.label]
		node := serviceNode[service.label]
		if state == "" {
			state = "[secondary]OFFLINE[-]"
			node = "-"
		}
		if strings.Contains(service.label, "Nomad") && serviceState[service.label] != "" {
			nomadPresent = true
		}
		fmt.Fprintf(&b, "%-13s %s [secondary]@ %s[-]\n", service.label, state, truncate(node, 14))
	}
	if nomadPresent {
		b.WriteString("[info]Nomad integration[-]: [success]reachable[-] (inventory-derived)\n")
	} else {
		b.WriteString("[info]Nomad integration[-]: [warning]offline[-]\n")
	}
	if !hd.compactMode {
		b.WriteString("[secondary]Tip: n/g/t jumps to source page for action details[-]")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (hd *HomeDashboard) pushTrend(cpu, memory, storage float64) {
	hd.cpuTrend = appendPoint(hd.cpuTrend, cpu)
	hd.memoryTrend = appendPoint(hd.memoryTrend, memory)
	hd.storageTrend = appendPoint(hd.storageTrend, storage)
}

func appendPoint(points []float64, value float64) []float64 {
	if value < 0 {
		value = 0
	}
	if value > 100 {
		value = 100
	}
	points = append(points, value)
	if len(points) > maxTrendPoints {
		return points[len(points)-maxTrendPoints:]
	}
	return points
}

func sparkline(points []float64) string {
	if len(points) == 0 {
		return "[secondary]······[-]"
	}
	bars := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	var out strings.Builder
	for _, v := range points {
		idx := int((v / 100.0) * float64(len(bars)-1))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(bars) {
			idx = len(bars) - 1
		}
		out.WriteRune(bars[idx])
	}
	return fmt.Sprintf("[primary]%s[-]", out.String())
}

func (hd *HomeDashboard) calculateHealth(cluster *api.Cluster, tasks []*api.ClusterTask, activeOps int) (int, int) {
	health := 100
	incidents := 0
	if cluster.TotalNodes > 0 && cluster.OnlineNodes < cluster.TotalNodes {
		incidents += cluster.TotalNodes - cluster.OnlineNodes
		health -= 20
	}
	if cluster.TotalNodes > 1 && !cluster.Quorate {
		incidents++
		health -= 25
	}
	if cluster.CPUUsage*100 > 85 {
		incidents++
		health -= 12
	}
	if utils.CalculatePercentage(cluster.MemoryUsed, cluster.MemoryTotal) > 90 {
		incidents++
		health -= 12
	}
	if utils.CalculatePercentageInt(cluster.StorageUsed, cluster.StorageTotal) > 92 {
		incidents++
		health -= 10
	}
	if recentFailures(tasks, 20*time.Minute) > 0 {
		incidents++
		health -= 12
	}
	if activeOps > 4 {
		health -= 5
	}
	if health < 0 {
		health = 0
	}
	return health, incidents
}

func recentFailures(tasks []*api.ClusterTask, window time.Duration) int {
	cutoff := time.Now().Add(-window).Unix()
	count := 0
	for _, task := range tasks {
		if task == nil || task.StartTime < cutoff {
			continue
		}
		if isTaskFailure(task.Status) {
			count++
		}
	}
	return count
}

func isTaskFailure(status string) bool {
	s := strings.ToLower(strings.TrimSpace(status))
	return strings.Contains(s, "error") || strings.Contains(s, "fail") || strings.Contains(s, "timeout") || strings.Contains(s, "aborted")
}

func findLastBackupSuccess(tasks []*api.ClusterTask, prev time.Time) time.Time {
	best := prev
	for _, task := range tasks {
		if task == nil || task.EndTime <= 0 {
			continue
		}
		taskType := strings.ToLower(task.Type)
		if !(strings.Contains(taskType, "backup") || strings.Contains(taskType, "vzdump")) {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(task.Status))
		if strings.Contains(status, "ok") || status == "stopped" || status == "success" {
			t := time.Unix(task.EndTime, 0)
			if t.After(best) {
				best = t
			}
		}
	}
	return best
}

func formatSince(t time.Time) string {
	if t.IsZero() {
		return "N/A"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func usageTag(v float64) string {
	switch {
	case v >= 95:
		return "[error]"
	case v >= 80:
		return "[warning]"
	default:
		return "[success]"
	}
}

func topGuests(vms []*api.VM, metric func(*api.VM) float64) []string {
	type entry struct {
		name  string
		value float64
	}
	var items []entry
	for _, vm := range vms {
		if vm == nil || vm.Template {
			continue
		}
		val := metric(vm)
		if val <= 0 {
			continue
		}
		items = append(items, entry{name: vm.Name, value: val})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].value > items[j].value
	})
	if len(items) == 0 {
		return []string{"[secondary]n/a[-]"}
	}
	limit := 3
	if len(items) < limit {
		limit = len(items)
	}
	out := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, fmt.Sprintf("[primary]%s[-] [secondary](%.1f%%)[-]", truncate(items[i].name, 14), items[i].value))
	}
	return out
}

func recentTaskSummary(tasks []*api.ClusterTask, typeKeywords []string, limit int) []string {
	sorted := append([]*api.ClusterTask(nil), tasks...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].StartTime > sorted[j].StartTime
	})
	lines := make([]string, 0, limit)
	for _, task := range sorted {
		if task == nil {
			continue
		}
		taskType := strings.ToLower(task.Type)
		match := false
		for _, k := range typeKeywords {
			if strings.Contains(taskType, k) {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		when := "now"
		if task.StartTime > 0 {
			when = time.Unix(task.StartTime, 0).Format("15:04")
		}
		lines = append(lines, fmt.Sprintf("  [info]%s[-] [primary]%s[-] [secondary]%s[-]", when, truncate(task.Type, 18), truncate(task.Status, 14)))
		if len(lines) >= limit {
			break
		}
	}
	if len(lines) == 0 {
		return []string{"  [secondary]No recent lifecycle events[-]"}
	}
	return lines
}

func taskStatusDecor(status string, endTime int64) (string, string) {
	s := strings.ToLower(strings.TrimSpace(status))
	switch {
	case isTaskFailure(s):
		return "[error]✖[-]", "error"
	case s == "" || endTime <= 0 || strings.Contains(s, "running"):
		return "[warning]◷[-]", "warning"
	default:
		return "[success]✔[-]", "success"
	}
}

func truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	if maxLen == 1 {
		return "…"
	}
	return string(r[:maxLen-1]) + "…"
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
