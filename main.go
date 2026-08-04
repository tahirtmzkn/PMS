package main

import (
	_ "embed"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
)

// appDisplayName is the name the user sees: the window title and the desktop
// entry. It is deliberately not the same string as the Debian package or the
// binary (both "pinginfomanager", lowercase because Debian requires it) or the
// config directory.
const appDisplayName = "PingInfoManager"

//go:embed assets/ping-pong.png
var pingPongPNG []byte

//go:embed assets/trash.png
var trashPNG []byte

func main() {
	app.SetMetadata(fyne.AppMetadata{
		ID:         "dev.tahir.pinginfomanager",
		Name:       "PingInfoManager",
		Migrations: map[string]bool{"fyneDo": true},
	})

	a := app.New()
	pingPongIcon := fyne.NewStaticResource("ping-pong.png", pingPongPNG)
	trashIcon := fyne.NewStaticResource("trash.png", trashPNG)
	a.SetIcon(pingPongIcon)

	win := a.NewWindow(appDisplayName)

	pm := newAppState(win, trashIcon)
	if path, err := defaultConfigPath(); err != nil {
		// No usable config directory: the app still runs, the list and the theme
		// choice just won't outlive it.
		fyne.LogError("no config directory, the configuration will not be saved", err)
	} else {
		pm.configFile = path
		// Before the first read, and only ever when the new file is absent: the
		// rename moved the config directory, and without this the saved device
		// list would look like a first run to the renamed app.
		if legacy, lerr := legacyConfigPath(); lerr == nil {
			if err := migrateLegacyConfig(path, legacy); err != nil {
				fyne.LogError("could not carry the configuration over from "+legacy, err)
			}
		}
	}

	// One read of the config file, applied in two parts. The theme goes on
	// first, before any widget is built, so the very first paint is already in
	// the chosen palette — and it installs the app's theme (the custom
	// green/yellow) whether or not a choice was saved.
	saved := pm.loadSavedConfig()
	pm.applyThemeMode(themeModeFromConfig(saved.Theme))

	win.SetContent(pm.buildUI(pingPongIcon))
	// The saved list goes in after the content is built, so the settings row's
	// theme Select is already in place and the status label exists. The hostname
	// lookups it starts call fyne.Do before the event loop is up, which is fine
	// — the driver queues them until it starts.
	pm.restoreDevices(saved.Devices)
	win.Resize(fyne.NewSize(1200, 700))
	win.ShowAndRun()
}
