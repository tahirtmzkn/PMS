package main

import (
	_ "embed"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
)

//go:embed assets/ping-pong.png
var pingPongPNG []byte

//go:embed assets/trash.png
var trashPNG []byte

func main() {
	app.SetMetadata(fyne.AppMetadata{
		ID:         "dev.tahir.pms",
		Name:       "PMS",
		Migrations: map[string]bool{"fyneDo": true},
	})

	a := app.New()
	a.Settings().SetTheme(newAppTheme())
	pingPongIcon := fyne.NewStaticResource("ping-pong.png", pingPongPNG)
	trashIcon := fyne.NewStaticResource("trash.png", trashPNG)
	a.SetIcon(pingPongIcon)

	win := a.NewWindow("PMS")

	pm := newAppState(win, trashIcon)
	if path, err := defaultConfigPath(); err != nil {
		// No usable config directory: the app still runs, the list just won't
		// outlive it.
		fyne.LogError("no config directory, the device list will not be saved", err)
	} else {
		pm.configFile = path
	}

	win.SetContent(pm.buildUI(pingPongIcon))
	// The saved list goes in after the content is built, so the rows the
	// hostname lookups write into exist. Those lookups call fyne.Do before the
	// event loop is up, which is fine — the driver queues them until it starts.
	pm.restoreDevices()
	win.Resize(fyne.NewSize(1200, 700))
	win.ShowAndRun()
}
