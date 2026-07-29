package main

import (
	"net"
	"sort"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// listInterfaces returns the machine's network interface names, sorted.
func listInterfaces() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(ifaces))
	for _, iface := range ifaces {
		names = append(names, iface.Name)
	}
	sort.Strings(names)
	return names
}

// buildSettingsPanel builds the always-visible settings row shown under the
// top bar. Each field applies immediately once it parses to a sane value;
// changing the interval or interface while running restarts the ticker so it
// takes effect on the next cycle instead of waiting for a manual Stop/Start.
//
// The entries deliberately have no Validator: Fyne renders a validation
// status icon inside any entry that has one, and out-of-range input is
// already handled by simply not applying it.
func (pm *appState) buildSettingsPanel() *fyne.Container {
	intervalEntry := widget.NewEntry()
	intervalEntry.SetText(strconv.Itoa(pm.pingInterval))
	intervalEntry.OnChanged = func(s string) {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || n < 1 {
			return
		}
		pm.pingInterval = n
		if pm.running {
			pm.startTicker()
		}
	}

	timeoutEntry := widget.NewEntry()
	timeoutEntry.SetText(strconv.Itoa(pm.pingTimeout))
	timeoutEntry.OnChanged = func(s string) {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || n < 100 || n > 10000 {
			return
		}
		pm.pingTimeout = n
	}

	pm.ifaceSelect = widget.NewSelect(listInterfaces(), func(s string) {
		if s == "" {
			return
		}
		pm.interfaceName = s
		if pm.running {
			pm.startTicker()
		}
	})
	pm.ifaceSelect.SetSelected(pm.interfaceName)

	pm.themeSelect = widget.NewSelect(themeModeLabels, func(label string) {
		pm.applyThemeMode(themeModeFromLabel(label))
		pm.persistConfig()
	})
	// Assigned rather than SetSelected: SetSelected fires OnChanged, and this
	// runs during buildUI — before the saved device list has been restored — so
	// the callback's persistConfig would write today's empty list over the
	// saved one. The mode itself is already applied by then (main.go does it
	// before building the UI), so there is nothing for the callback to do here.
	pm.themeSelect.Selected = pm.themeMode.label()

	h := pm.controlHeight
	return container.NewHBox(
		widget.NewLabel("Interval (s)"), sized(70, h, intervalEntry),
		widget.NewLabel("Timeout (ms)"), sized(90, h, timeoutEntry),
		widget.NewLabel("Interface"), sized(160, h, pm.ifaceSelect),
		widget.NewLabel("Theme"), sized(110, h, pm.themeSelect),
	)
}
