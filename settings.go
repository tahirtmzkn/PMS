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
	// The floor is 1ms rather than something defensible as a network timeout: how
	// tight a reply window to accept is the operator's call, and a device that
	// cannot answer inside it is exactly what the Fail column is for. Worth
	// knowing where the mechanism itself gives out, though: runBounded times the
	// budget from process start, so it also covers `ping`'s own setup before the
	// echo leaves. Measured on this desktop against a LAN device that answers in
	// well under a millisecond (~0.9ms wall per probe including spawn and exit),
	// 2ms still answered 20/20 while 1ms dropped one of 20 — at that point the
	// budget is being spent on ping starting up rather than on the network.
	timeoutEntry.OnChanged = func(s string) {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || n < 1 || n > 10000 {
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
