package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// appTheme tweaks Fyne's default palette so every success/warning-colored
// widget (buttons, row highlights) shares the same slightly darker green
// and a true yellow instead of Fyne's default orange-ish warning — one
// place defining "what green/yellow means" for the whole app.
type appTheme struct {
	fyne.Theme
}

func newAppTheme() fyne.Theme {
	return appTheme{Theme: theme.DefaultTheme()}
}

func (t appTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameSuccess:
		return color.NRGBA{R: 0x38, G: 0x8e, B: 0x3c, A: 0xff}
	case theme.ColorNameWarning:
		return color.NRGBA{R: 0xf9, G: 0xa8, B: 0x25, A: 0xff}
	}
	return t.Theme.Color(name, variant)
}
