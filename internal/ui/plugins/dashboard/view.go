package dashboard

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/devnullvoid/pvetui/internal/plugins/nomad"
	"github.com/devnullvoid/pvetui/internal/ui/components"
	"github.com/devnullvoid/pvetui/internal/ui/models"
	"github.com/devnullvoid/pvetui/internal/ui/utils"
	"github.com/devnullvoid/pvetui/pkg/api"
)

const dashboardPageName = "plugin.dashboard.main"

// dashboardView is the full-screen monitoring display.
type dashboardView struct {
	app         *components.App
	nomadClient *nomad.Client
	refreshSecs int

	// tview primitives
	layout      *tview.Flex
	titleBar    *tview.TextView
	nodesView   *tview.TextView
	summaryView *tview.TextView
	nomadView   *tview.TextView
	guestsView  *tview.TextView
	tasksView   *tview.TextView

	// state
	stopCh           chan struct{}
	lastNomadJobs    []nomad.Job
	nomadErr         error
	refreshCountdown int
}

func newDashboardView(app *components.App, nomadClient *nomad.Client, refreshSecs int) *dashboardView {
	v := &dashboardView{
		app:         app,
		nomadClient: nomadClient,
		refreshSecs: refreshSecs,
	}

	v.build()

	return v
}

// build assembles the tview layout once.
func (v *dashboardView) build() {
	v.titleBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter)
	v.titleBar.SetBackgroundColor(tcell.ColorDefault)

	v.nodesView = newPanel(" ◈ NODES ")
	v.summaryView = newPanel(" ◈ CLUSTER ")

	if v.nomadClient != nil {
		v.nomadView = newPanel(" ◈ NOMAD JOBS ")
	} else {
		v.nomadView = newPanel(" ◈ RESOURCE MAP ")
	}

	v.guestsView = newPanel(" ◈ GUESTS ")
	v.tasksView = newPanel(" ◈ RECENT TASKS ")

	topRow := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(v.nodesView, 0, 3, false).
		AddItem(v.summaryView, 0, 2, false).
		AddItem(v.nomadView, 0, 4, false)

	bottomRow := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(v.guestsView, 0, 5, false).
		AddItem(v.tasksView, 0, 3, false)

	v.layout = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(v.titleBar, 1, 0, false).
		AddItem(topRow, 0, 5, false).
		AddItem(bottomRow, 0, 6, false)
}

// newPanel creates a styled tview.TextView suitable for a dashboard panel.
func newPanel(title string) *tview.TextView {
	tv := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(false).
		SetWrap(false)
	tv.SetBorder(true).
		SetBorderColor(tcell.ColorTeal).
		SetTitleColor(tcell.ColorAqua).
		SetTitleAlign(tview.AlignLeft).
		SetTitle(title).
		SetBackgroundColor(tcell.ColorDefault)

	return tv
}

// show adds the dashboard page to the app and starts auto-refresh.
func (v *dashboardView) show() {
	v.stopCh = make(chan struct{})
	v.refreshCountdown = v.refreshSecs

	v.renderAll(nil)

	v.app.Pages().AddPage(dashboardPageName, v.layout, true, true)
	v.app.SetFocus(v.layout)

	v.app.SetHotkeyOverride(func(event *tcell.EventKey) *tcell.EventKey {
		switch {
		case event.Key() == tcell.KeyEscape,
			event.Key() == tcell.KeyRune && event.Rune() == 'q':
			v.hide()
		case event.Key() == tcell.KeyRune && event.Rune() == 'r':
			v.triggerRefresh()
		}

		return nil
	})

	v.startBackground()
}

// hide closes the dashboard and restores normal keyboard handling.
func (v *dashboardView) hide() {
	v.app.SetHotkeyOverride(nil)
	v.stop()
	v.app.Pages().RemovePage(dashboardPageName)
}

// stop halts the background refresh goroutine.
func (v *dashboardView) stop() {
	if v.stopCh != nil {
		select {
		case <-v.stopCh:
		default:
			close(v.stopCh)
		}
		v.stopCh = nil
	}
}

// triggerRefresh immediately re-fetches data and redraws.
func (v *dashboardView) triggerRefresh() {
	v.refreshCountdown = v.refreshSecs
	go v.fetchAndDraw()
}

// startBackground starts the refresh ticker goroutine.
func (v *dashboardView) startBackground() {
	stopCh := v.stopCh
	go func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()

		for {
			select {
			case <-stopCh:
				return
			case <-tick.C:
				v.refreshCountdown--
				if v.refreshCountdown <= 0 {
					v.refreshCountdown = v.refreshSecs
					go v.fetchAndDraw()
				} else {
					v.app.QueueUpdateDraw(func() {
						v.titleBar.SetText(v.renderTitle())
					})
				}
			}
		}
	}()
}

// fetchAndDraw fetches Nomad data (if configured) then redraws all panels.
func (v *dashboardView) fetchAndDraw() {
	var jobs []nomad.Job
	var nomadErr error

	if v.nomadClient != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		jobs, nomadErr = v.nomadClient.Jobs(ctx)
	}

	v.app.QueueUpdateDraw(func() {
		v.renderAll(jobs)

		if v.nomadClient != nil {
			v.nomadErr = nomadErr
			if nomadErr == nil {
				v.lastNomadJobs = jobs
			}
		}
	})
}

// renderAll rebuilds and sets text for every panel. If jobs is nil, uses the
// last cached Nomad job list.
func (v *dashboardView) renderAll(jobs []nomad.Job) {
	if jobs == nil {
		jobs = v.lastNomadJobs
	}

	v.titleBar.SetText(v.renderTitle())
	v.nodesView.SetText(v.renderNodes())
	v.summaryView.SetText(v.renderSummary())
	v.nomadView.SetText(v.renderNomadOrMap(jobs))
	v.guestsView.SetText(v.renderGuests())
	v.tasksView.SetText(v.renderTasks())
}

// ─── Render helpers ───────────────────────────────────────────────────────────

func (v *dashboardView) renderTitle() string {
	now := time.Now().Format("Mon 02 Jan 2006  15:04:05")

	clusterName := "pvetui"
	if v.app.Client() != nil && v.app.Client().Cluster != nil {
		clusterName = v.app.Client().Cluster.Name
	}

	countdown := fmt.Sprintf("[aqua]↻[-] [green]%ds[-]", v.refreshCountdown)
	if v.refreshCountdown <= 3 {
		countdown = fmt.Sprintf("[aqua]↻[-] [yellow]%ds[-]", v.refreshCountdown)
	}

	return fmt.Sprintf(
		"[aqua::b]◈ PROXMOX COMMAND CENTER[-]  [gray]●[-]  [white::b]%s[-]  [gray]●[-]  [yellow]%s[-]  [gray]│[-]  %s  [gray](ESC/q=close  r=refresh)[-]",
		clusterName, now, countdown,
	)
}

func (v *dashboardView) renderNodes() string {
	nodes := models.GlobalState.OriginalNodes
	if len(nodes) == 0 {
		return "\n  [gray]Loading node data...[-]"
	}

	var sb strings.Builder

	for _, node := range nodes {
		if node == nil {
			continue
		}

		onlineDot := "[green]●[-]"
		statusLabel := "[green]ONLINE[-]"
		if !node.Online {
			onlineDot = "[red]○[-]"
			statusLabel = "[red]OFFLN[-]"
		}

		fmt.Fprintf(&sb, "\n %s [white::b]%-12s[-] %s\n",
			onlineDot, truncate(node.Name, 12), statusLabel)

		if node.Online {
			cpuPct := node.CPUUsage * 100
			memPct := utils.CalculatePercentage(node.MemoryUsed, node.MemoryTotal)

			fmt.Fprintf(&sb, "   [gray]CPU[gray] %s [%s]%4.1f%%[-]\n",
				progressBar(cpuPct, 10), barColorName(cpuPct), cpuPct)
			fmt.Fprintf(&sb, "   [gray]MEM[gray] %s [%s]%4.1f%%[-]\n",
				progressBar(memPct, 10), barColorName(memPct), memPct)
		} else {
			sb.WriteString("   [gray]CPU [----------]   N/A[-]\n")
			sb.WriteString("   [gray]MEM [----------]   N/A[-]\n")
		}
	}

	return sb.String()
}

func (v *dashboardView) renderSummary() string {
	nodes := models.GlobalState.OriginalNodes
	vms := models.GlobalState.OriginalVMs

	var (
		onlineNodes                        int
		totalCPU, usedCPU                  float64
		totalMem, usedMem                  float64
		totalStorage, usedStorage          int64
		runningVMs, stoppedVMs, runningCTs int
		stoppedCTs                         int
	)

	for _, n := range nodes {
		if n == nil {
			continue
		}
		if n.Online {
			onlineNodes++
			totalCPU += n.CPUCount
			usedCPU += n.CPUCount * n.CPUUsage
			totalMem += n.MemoryTotal
			usedMem += n.MemoryUsed
			totalStorage += n.TotalStorage * 1024 * 1024 * 1024
			usedStorage += n.UsedStorage * 1024 * 1024 * 1024
		}
	}

	for _, vm := range vms {
		if vm == nil {
			continue
		}
		switch {
		case vm.Type == api.VMTypeQemu && vm.Status == api.VMStatusRunning:
			runningVMs++
		case vm.Type == api.VMTypeQemu:
			stoppedVMs++
		case vm.Status == api.VMStatusRunning:
			runningCTs++
		default:
			stoppedCTs++
		}
	}

	cpuPct := utils.CalculatePercentage(usedCPU, totalCPU)
	memPct := utils.CalculatePercentage(usedMem, totalMem)
	stoPct := utils.CalculatePercentageInt(usedStorage, totalStorage)

	var sb strings.Builder

	fmt.Fprintf(&sb, "\n  [gray]Nodes   [white]%d[gray]/[white]%d[-] online\n\n",
		onlineNodes, len(nodes))

	fmt.Fprintf(&sb, "  [aqua]CPU[-]  %s\n", progressBar(cpuPct, 12))
	fmt.Fprintf(&sb, "  [gray]     [%s]%4.1f%%[-] / %.0f cores\n\n",
		barColorName(cpuPct), cpuPct, totalCPU)

	fmt.Fprintf(&sb, "  [aqua]MEM[-]  %s\n", progressBar(memPct, 12))
	fmt.Fprintf(&sb, "  [gray]     [%s]%4.1f%%[-] / %s\n\n",
		barColorName(memPct), memPct, utils.FormatBytesFloat(totalMem))

	fmt.Fprintf(&sb, "  [aqua]STO[-]  %s\n", progressBar(stoPct, 12))
	fmt.Fprintf(&sb, "  [gray]     [%s]%4.1f%%[-] / %s\n\n",
		barColorName(stoPct), stoPct, utils.FormatBytes(totalStorage))

	sb.WriteString("  [gray]──────────────────[-]\n")
	fmt.Fprintf(&sb, "  [green]VM[-][gray] run[white] %2d[-]  stp[white] %2d[-]\n", runningVMs, stoppedVMs)
	fmt.Fprintf(&sb, "  [aqua]CT[-][gray] run[white] %2d[-]  stp[white] %2d[-]\n", runningCTs, stoppedCTs)

	return sb.String()
}

func (v *dashboardView) renderNomadOrMap(jobs []nomad.Job) string {
	if v.nomadClient == nil {
		return v.renderResourceMap()
	}

	return v.renderNomadJobs(jobs)
}

func (v *dashboardView) renderNomadJobs(jobs []nomad.Job) string {
	if v.nomadErr != nil {
		return fmt.Sprintf("\n  [red]Nomad unreachable[-]\n\n  [gray]%s[-]", truncate(v.nomadErr.Error(), 40))
	}

	if len(jobs) == 0 {
		return "\n  [gray]No jobs found[-]"
	}

	sort.Slice(jobs, func(i, j int) bool {
		si, sj := jobStatusOrder(jobs[i].Status), jobStatusOrder(jobs[j].Status)
		if si != sj {
			return si < sj
		}
		return jobs[i].Name < jobs[j].Name
	})

	var sb strings.Builder

	sb.WriteString("\n")
	fmt.Fprintf(&sb, "  [aqua]%-18s %-7s %-7s %-6s[-]\n", "NAME", "TYPE", "STATUS", "ALLOCS")
	sb.WriteString("  [gray]────────────────────────────────────[-]\n")

	for _, j := range jobs {
		dot, statusStr := nomadStatusDot(j.Status)
		typeAbbr := nomadTypeAbbr(j.Type)
		allocs := fmt.Sprintf("%d/%d", j.Allocs.Running, j.Allocs.Running+j.Allocs.Queued+j.Allocs.Starting)
		if j.Status == "dead" {
			allocs = fmt.Sprintf("[gray]%d/%d[-]", j.Allocs.Running, j.Allocs.Total())
		}

		fmt.Fprintf(&sb, "  %s [white]%-18s[-] [gray]%-7s[-] %s %-6s\n",
			dot, truncate(j.Name, 18), typeAbbr, statusStr, allocs)
	}

	return sb.String()
}

func (v *dashboardView) renderResourceMap() string {
	nodes := models.GlobalState.OriginalNodes
	vms := models.GlobalState.OriginalVMs

	if len(nodes) == 0 {
		return "\n  [gray]Loading...[-]"
	}

	guestsByNode := make(map[string][]*api.VM)
	for _, vm := range vms {
		if vm != nil {
			guestsByNode[vm.Node] = append(guestsByNode[vm.Node], vm)
		}
	}

	var sb strings.Builder
	sb.WriteString("\n")

	for _, node := range nodes {
		if node == nil {
			continue
		}
		guests := guestsByNode[node.Name]
		running, stopped := 0, 0
		for _, g := range guests {
			if g.Status == api.VMStatusRunning {
				running++
			} else {
				stopped++
			}
		}

		dot := "[green]●[-]"
		if !node.Online {
			dot = "[red]○[-]"
		}

		fmt.Fprintf(&sb, " %s [white::b]%s[-]\n", dot, node.Name)
		fmt.Fprintf(&sb, "   [green]▶ %2d running[-]  [gray]■ %2d stopped[-]\n\n", running, stopped)

		for i, g := range guests {
			if i >= 6 {
				fmt.Fprintf(&sb, "   [gray]... %d more[-]\n", len(guests)-6)
				break
			}
			gDot := vmStatusMark(g.Status)
			typeTag := "[gray]VM[-]"
			if g.Type == api.VMTypeLXC {
				typeTag = "[aqua]CT[-]"
			}
			fmt.Fprintf(&sb, "   %s %s [gray]%s[-]\n", gDot, typeTag, truncate(g.Name, 16))
		}
	}

	return sb.String()
}

func (v *dashboardView) renderGuests() string {
	vms := models.GlobalState.OriginalVMs
	if len(vms) == 0 {
		return "\n  [gray]Loading guest data...[-]"
	}

	running := make([]*api.VM, 0)
	stopped := make([]*api.VM, 0)

	for _, vm := range vms {
		if vm == nil {
			continue
		}
		if vm.Status == api.VMStatusRunning {
			running = append(running, vm)
		} else {
			stopped = append(stopped, vm)
		}
	}

	sort.Slice(running, func(i, j int) bool {
		return running[i].CPU > running[j].CPU
	})
	sort.Slice(stopped, func(i, j int) bool {
		return stopped[i].Name < stopped[j].Name
	})

	var sb strings.Builder
	sb.WriteString("\n")
	fmt.Fprintf(&sb, "  [aqua]%-4s %-5s %-20s %-8s  %-22s  %-22s[-]\n",
		"TYPE", "ID", "NAME", "NODE", "CPU", "MEM")
	sb.WriteString("  [gray]──────────────────────────────────────────────────────────────────────────────[-]\n")

	for _, vm := range running {
		cpuPct := vm.CPU * 100
		memPct := utils.CalculatePercentage(float64(vm.Mem), float64(vm.MaxMem))
		renderGuestRow(&sb, vm, cpuPct, memPct)
	}

	if len(stopped) > 0 {
		sb.WriteString("  [gray]── stopped ─────────────────────────────────────────────────────────────────────[-]\n")
		for i, vm := range stopped {
			if i >= 8 {
				fmt.Fprintf(&sb, "  [gray]  ... %d more stopped[-]\n", len(stopped)-8)
				break
			}
			renderStoppedRow(&sb, vm)
		}
	}

	return sb.String()
}

func renderGuestRow(sb *strings.Builder, vm *api.VM, cpuPct, memPct float64) {
	typeStr := "[white]VM[-]"
	if vm.Type == api.VMTypeLXC {
		typeStr = "[aqua]CT[-]"
	}

	cpuBar := fmt.Sprintf("%s [%s]%4.1f%%[-]", progressBar(cpuPct, 8), barColorName(cpuPct), cpuPct)
	memBar := fmt.Sprintf("%s [%s]%4.1f%%[-]", progressBar(memPct, 8), barColorName(memPct), memPct)

	fmt.Fprintf(sb, "  %s [gray]%-5d[-] [white]%-20s[-] [gray]%-8s[-]  %s  %s\n",
		typeStr, vm.ID, truncate(vm.Name, 20), truncate(vm.Node, 8), cpuBar, memBar)
}

func renderStoppedRow(sb *strings.Builder, vm *api.VM) {
	typeStr := "[gray]VM[-]"
	if vm.Type == api.VMTypeLXC {
		typeStr = "[gray]CT[-]"
	}

	fmt.Fprintf(sb, "  %s [gray]%-5d %-20s %-8s  ■ stopped[-]\n",
		typeStr, vm.ID, truncate(vm.Name, 20), truncate(vm.Node, 8))
}

func (v *dashboardView) renderTasks() string {
	tasks := models.GlobalState.OriginalTasks
	if len(tasks) == 0 {
		return "\n  [gray]No recent tasks[-]"
	}

	var sb strings.Builder
	sb.WriteString("\n")
	fmt.Fprintf(&sb, "  [aqua]%-12s %-14s %-8s %-10s[-]\n", "STATUS", "TYPE", "NODE", "STARTED")
	sb.WriteString("  [gray]────────────────────────────────────────[-]\n")

	limit := 12
	if len(tasks) < limit {
		limit = len(tasks)
	}

	for _, task := range tasks[:limit] {
		if task == nil {
			continue
		}

		statusStr, dot := formatTaskStatus(task)
		startTime := ""
		if task.StartTime > 0 {
			t := time.Unix(task.StartTime, 0)
			startTime = t.Format("15:04:05")
		}

		fmt.Fprintf(&sb, "  %s %s [gray]%-14s[-] [white]%-8s[-] [gray]%s[-]\n",
			dot, statusStr, truncate(task.Type, 14), truncate(task.Node, 8), startTime)
	}

	return sb.String()
}

// ─── Formatting helpers ───────────────────────────────────────────────────────

func progressBar(pct float64, width int) string {
	if pct < 0 {
		pct = 0
	}

	if pct > 100 {
		pct = 100
	}

	filled := int(pct / 100.0 * float64(width))
	if filled > width {
		filled = width
	}

	col := barColorName(pct)

	return fmt.Sprintf("[%s]%s[-][gray]%s[-]",
		col,
		strings.Repeat("█", filled),
		strings.Repeat("░", width-filled),
	)
}

func barColorName(pct float64) string {
	switch {
	case pct < 50:
		return "green"
	case pct < 75:
		return "yellow"
	case pct < 90:
		return "red"
	default:
		return "fuchsia"
	}
}

func vmStatusMark(status string) string {
	switch status {
	case api.VMStatusRunning:
		return "[green]▶[-]"
	case "paused":
		return "[yellow]◐[-]"
	default:
		return "[gray]■[-]"
	}
}

func nomadStatusDot(status string) (dot, label string) {
	switch status {
	case "running":
		return "[green]●[-]", "[green]RUN  [-]"
	case "pending":
		return "[yellow]◐[-]", "[yellow]PEND [-]"
	default:
		return "[gray]○[-]", "[gray]DEAD [-]"
	}
}

func nomadTypeAbbr(t string) string {
	switch t {
	case "service":
		return "svc"
	case "batch":
		return "bat"
	case "system":
		return "sys"
	case "sysbatch":
		return "sbat"
	default:
		return t
	}
}

func jobStatusOrder(status string) int {
	switch status {
	case "running":
		return 0
	case "pending":
		return 1
	default:
		return 2
	}
}

func formatTaskStatus(task *api.ClusterTask) (label, dot string) {
	isRunning := task.EndTime == 0 && task.StartTime > 0
	hasFailed := strings.EqualFold(task.Status, "error") ||
		strings.HasPrefix(strings.ToLower(task.Status), "fail")

	switch {
	case isRunning:
		return "[yellow]RUNNING[-]    ", "[yellow]⟳[-]"
	case hasFailed:
		return "[red]FAILED[-]     ", "[red]✗[-]"
	case task.EndTime > 0:
		return "[green]OK[-]         ", "[green]✓[-]"
	default:
		return "[gray]PENDING[-]    ", "[gray]·[-]"
	}
}

func truncate(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}

	if limit <= 1 {
		return "…"
	}

	return string(runes[:limit-1]) + "…"
}
