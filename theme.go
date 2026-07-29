package main

import (
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// themeMode is the app's light/dark choice: follow the desktop, or pin one.
type themeMode int

const (
	themeSystem themeMode = iota
	themeLight
	themeDark
)

// themeModeLabels are the labels shown in the settings row, indexed by
// themeMode — so this slice's order is the constant order above and also the
// order the Select lists them in.
var themeModeLabels = []string{"System", "Light", "Dark"}

func (m themeMode) label() string {
	if m < 0 || int(m) >= len(themeModeLabels) {
		return themeModeLabels[themeSystem]
	}
	return themeModeLabels[m]
}

func themeModeFromLabel(label string) themeMode {
	for i, l := range themeModeLabels {
		if l == label {
			return themeMode(i)
		}
	}
	return themeSystem
}

// configValue is what the mode is written as in the config file. themeSystem
// writes nothing (the field is omitempty), so following the desktop — the
// default — leaves the file exactly as it would have been without this setting.
func (m themeMode) configValue() string {
	switch m {
	case themeLight:
		return "light"
	case themeDark:
		return "dark"
	}
	return ""
}

// themeModeFromConfig reads that field back. Anything unrecognised — a missing
// field, an explicit "system", a hand-edited typo — means follow the desktop,
// which is the one choice that is never wrong to fall back to.
func themeModeFromConfig(value string) themeMode {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "light":
		return themeLight
	case "dark":
		return themeDark
	}
	return themeSystem
}

// variant is the Fyne variant this mode pins, and false for themeSystem, which
// pins nothing.
func (m themeMode) variant() (fyne.ThemeVariant, bool) {
	switch m {
	case themeLight:
		return theme.VariantLight, true
	case themeDark:
		return theme.VariantDark, true
	}
	return 0, false
}

// appTheme tweaks Fyne's default palette so every success/warning-colored
// widget (buttons, row highlights) shares the same slightly darker green
// and a true yellow instead of Fyne's default orange-ish warning — one
// place defining "what green/yellow means" for the whole app — and pins the
// light/dark variant when the user has picked one.
type appTheme struct {
	fyne.Theme
	mode themeMode
}

func newAppTheme(mode themeMode) fyne.Theme {
	return appTheme{Theme: theme.DefaultTheme(), mode: mode}
}

// Color is where "dark mode" happens. Fyne works out the variant from the
// desktop and hands it to every Color call; substituting our own here is enough
// to flip the whole app, because Fyne's default theme picks its light or dark
// palette purely from this argument and every color the app asks for by name
// (including theme.SuccessColor()/ErrorColor() in ui.go) comes through here.
func (t appTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	if pinned, ok := t.mode.variant(); ok {
		variant = pinned
	}

	// Deliberately the same green and yellow in both variants: these say
	// "up" and "caution", and that meaning shouldn't shift with the palette.
	switch name {
	case theme.ColorNameSuccess:
		return color.NRGBA{R: 0x38, G: 0x8e, B: 0x3c, A: 0xff}
	case theme.ColorNameWarning:
		return color.NRGBA{R: 0xf9, G: 0xa8, B: 0x25, A: 0xff}
	}
	return t.Theme.Color(name, variant)
}
