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
	pingPongIcon := fyne.NewStaticResource("ping-pong.png", pingPongPNG)
	trashIcon := fyne.NewStaticResource("trash.png", trashPNG)
	a.SetIcon(pingPongIcon)

	win := a.NewWindow("PMS")

	pm := newAppState(win, trashIcon)
	win.SetContent(pm.buildUI(pingPongIcon))
	win.Resize(fyne.NewSize(1200, 700))
	win.ShowAndRun()
}
