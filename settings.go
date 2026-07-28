package main

import (
	"fmt"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
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

func (pm *appState) openSettings() {
	intervalEntry := widget.NewEntry()
	intervalEntry.SetText(strconv.Itoa(pm.pingInterval))
	intervalEntry.Validator = intRangeValidator(1, 0)

	timeoutEntry := widget.NewEntry()
	timeoutEntry.SetText(strconv.Itoa(pm.pingTimeout))
	timeoutEntry.Validator = intRangeValidator(100, 10000)

	ifaceEntry := widget.NewEntry()
	ifaceEntry.SetText(pm.interfaceName)

	items := []*widget.FormItem{
		widget.NewFormItem("Ping Interval (s)", intervalEntry),
		widget.NewFormItem("Ping Timeout (ms)", timeoutEntry),
		widget.NewFormItem("Network Interface", ifaceEntry),
	}

	dialog.NewForm("Settings", "Save", "Cancel", items, func(confirmed bool) {
		if !confirmed {
			return
		}
		interval, _ := strconv.Atoi(intervalEntry.Text)
		timeout, _ := strconv.Atoi(timeoutEntry.Text)
		iface := strings.TrimSpace(ifaceEntry.Text)

		pm.pingInterval = interval
		pm.pingTimeout = timeout
		if iface != "" {
			pm.interfaceName = iface
		}
		if pm.running {
			pm.startTicker()
		}
	}, pm.win).Show()
}
