package main

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// intRangeValidator returns a validator requiring a whole number in [min, max].
// A max of 0 means "no upper bound".
func intRangeValidator(min, max int) fyne.StringValidator {
	return func(s string) error {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			return fmt.Errorf("must be a whole number")
		}
		if n < min || (max > 0 && n > max) {
			if max > 0 {
				return fmt.Errorf("must be between %d and %d", min, max)
			}
			return fmt.Errorf("must be at least %d", min)
		}
		return nil
	}
}

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
// top bar. Each field applies immediately once valid; changing the interval
// or interface while running restarts the ticker so it takes effect on the
// next cycle instead of waiting for a manual Stop/Start.
func (pm *appState) buildSettingsPanel() *fyne.Container {
	intervalEntry := widget.NewEntry()
	intervalEntry.SetText(strconv.Itoa(pm.pingInterval))
	intervalEntry.Validator = intRangeValidator(1, 0)
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
	timeoutEntry.Validator = intRangeValidator(100, 10000)
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

	return container.NewHBox(
		widget.NewLabel("Interval (s)"), fixedWidth(70, intervalEntry),
		widget.NewLabel("Timeout (ms)"), fixedWidth(90, timeoutEntry),
		widget.NewLabel("Interface"), fixedWidth(160, pm.ifaceSelect),
	)
}
