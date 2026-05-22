// Package dashboard provides a full-screen monitoring dashboard plugin for pvetui.
// It shows Proxmox cluster nodes, guests, tasks, and optionally Nomad jobs in a
// single dense read-only view designed to run on a dedicated display.
package dashboard

import (
	"context"

	"github.com/devnullvoid/pvetui/internal/plugins/nomad"
	"github.com/devnullvoid/pvetui/internal/ui/components"
)

// PluginID identifies the dashboard plugin for configuration toggles.
const PluginID = "dashboard"

// Plugin implements the dashboard monitoring view.
type Plugin struct {
	app  *components.App
	view *dashboardView
}

// New returns a fresh dashboard plugin instance.
func New() *Plugin {
	return &Plugin{}
}

// ID returns the stable plugin identifier.
func (p *Plugin) ID() string { return PluginID }

// Name returns the human-readable plugin name.
func (p *Plugin) Name() string { return "Monitoring Dashboard" }

// Description describes the plugin.
func (p *Plugin) Description() string {
	return "Full-screen cluster monitoring dashboard with nodes, guests, tasks, and Nomad jobs."
}

// Initialize sets up the plugin and stores the app reference.
func (p *Plugin) Initialize(_ context.Context, app *components.App, _ components.PluginRegistrar) error {
	p.app = app

	cfg := app.Config().Plugins.Dashboard
	refreshSecs := cfg.RefreshSecs
	if refreshSecs <= 0 {
		refreshSecs = 15
	}

	var nomadClient *nomad.Client
	if cfg.NomadAddr != "" {
		nomadClient = nomad.New(cfg.NomadAddr, cfg.NomadToken)
	}

	p.view = newDashboardView(app, nomadClient, refreshSecs)

	return nil
}

// Shutdown tears down the plugin and stops any background work.
func (p *Plugin) Shutdown(_ context.Context) error {
	if p.view != nil {
		p.view.stop()
	}

	p.app = nil

	return nil
}

// ModalPageNames returns the page names this plugin registers as modals.
func (p *Plugin) ModalPageNames() []string {
	return []string{dashboardPageName}
}

// OpenGlobal is called from the global menu to open the dashboard.
func (p *Plugin) OpenGlobal(_ context.Context, app *components.App) error {
	p.view.show()

	return nil
}
