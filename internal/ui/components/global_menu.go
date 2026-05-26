package components

import (
	"sort"

	"github.com/devnullvoid/pvetui/internal/version"
	"github.com/gdamore/tcell/v2"

	"github.com/devnullvoid/pvetui/internal/ui/models"
)

// ShowGlobalContextMenu displays the global context menu for app-wide actions.
func (a *App) ShowGlobalContextMenu() {
	// Store last focused primitive
	a.lastFocus = a.GetFocus()

	// Collect plugins that expose a global action, sorted by name for stable ordering.
	type globalEntry struct {
		name   string
		id     string
		plugin GlobalActionPlugin
	}

	var globalEntries []globalEntry
	for id, pl := range a.plugins {
		if pl == nil {
			continue
		}
		if gp, ok := pl.(GlobalActionPlugin); ok {
			globalEntries = append(globalEntries, globalEntry{name: pl.Name(), id: id, plugin: gp})
		}
	}
	sort.Slice(globalEntries, func(i, j int) bool {
		return globalEntries[i].name < globalEntries[j].name
	})

	// Build menu items, shortcuts, and a handler map.
	menuItems := []string{
		"Connection Profiles",
		"Manage Plugins",
	}
	shortcuts := []rune{'p', 'm'}

	for _, e := range globalEntries {
		menuItems = append(menuItems, e.name)
		shortcuts = append(shortcuts, 0) // no single-key shortcut for dynamic entries
	}

	menuItems = append(menuItems,
		"Refresh All Data",
		"Toggle Auto-Refresh",
		"Help",
		"About",
		"Quit",
	)
	shortcuts = append(shortcuts, 'r', 'a', '?', 'i', 'q')

	// Build a name→handler map so the switch below stays O(1) for static entries
	// and global-plugin entries are handled by a single loop.
	menu := NewContextMenuWithShortcuts(" Global Actions ", menuItems, shortcuts, func(index int, action string) {
		a.CloseContextMenu()

		// Static built-in actions
		switch action {
		case "Connection Profiles":
			a.showConnectionProfilesDialog()
			return
		case "Manage Plugins":
			a.showManagePluginsDialog()
			return
		case "Refresh All Data":
			if models.GlobalState.HasPendingOperations() {
				a.showMessageSafe("Cannot refresh data while there are pending operations in progress")
				return
			}
			a.manualRefresh()
			return
		case "Toggle Auto-Refresh":
			a.toggleAutoRefresh()
			return
		case "Help":
			if a.pages.HasPage("help") {
				a.helpModal.Hide()
			} else {
				a.helpModal.Show()
			}
			return
		case "About":
			a.showAboutDialog()
			return
		case "Quit":
			a.showQuitConfirmation()
			return
		}

		// Dynamic plugin actions
		for _, e := range globalEntries {
			if action == e.name {
				if err := e.plugin.OpenGlobal(a.ctx, a); err != nil {
					a.showMessageSafe(e.name + " failed: " + err.Error())
				}
				return
			}
		}
	})
	menu.SetApp(a)

	menuList := menu.Show()

	// Add input capture to close menu on Escape or 'h'
	oldCapture := menuList.GetInputCapture()
	menuList.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape || (event.Key() == tcell.KeyRune && event.Rune() == 'h') {
			a.CloseContextMenu()
			return nil
		}

		if oldCapture != nil {
			return oldCapture(event)
		}

		return event
	})

	a.showContextMenuPage(menuList, menuItems, 30, false, nil)
}

// showAboutDialog displays information about the application.
func (a *App) showAboutDialog() {
	// Get version information
	versionInfo := version.GetBuildInfo()

	// Create about dialog using the reusable function
	modal := CreateAboutDialog(versionInfo, func() {
		a.pages.RemovePage("about")
	})

	a.pages.AddPage("about", modal, false, true)
}
