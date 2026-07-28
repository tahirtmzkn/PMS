package main

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func TestSmokeBuildUI(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()

	win := a.NewWindow("PMS")
	pm := newAppState(win, fyne.NewStaticResource("trash.png", trashPNG))
	content := pm.buildUI(fyne.NewStaticResource("ping-pong.png", pingPongPNG))
	win.SetContent(content)
	win.Resize(fyne.NewSize(1200, 700))

	pm.addDevice("10.0.0.2", "")
	pm.addDevice("9.0.0.1", "Beta")
	if len(pm.devices) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(pm.devices))
	}
	if pm.devices[0].Name != "Unknown" {
		t.Errorf("default name = %q, want Unknown", pm.devices[0].Name)
	}

	// numeric IP ordering, not lexicographic
	pm.sortBy(sortIP)
	if pm.devices[0].IP != "9.0.0.1" {
		t.Errorf("ip sort asc first = %q, want 9.0.0.1", pm.devices[0].IP)
	}
	pm.sortBy(sortIP)
	if pm.devices[0].IP != "10.0.0.2" {
		t.Errorf("ip sort desc first = %q, want 10.0.0.2", pm.devices[0].IP)
	}

	if got := formatLoss(10, 20); got != "%50" {
		t.Errorf("formatLoss = %q", got)
	}
	if got := formatLoss(1, 1000); got != "%0.1" {
		t.Errorf("formatLoss fraction = %q", got)
	}
	if got := formatLoss(0, 0); got != "-" {
		t.Errorf("formatLoss unpinged = %q", got)
	}

	// Loss sorts on the fail/total ratio, not the raw fail count, and
	// never-pinged devices stay grouped ahead of measured ones.
	pm.devices[0].Fail, pm.devices[0].Total = 1, 10 // 10%
	pm.devices[1].Fail, pm.devices[1].Total = 5, 10 // 50%
	pm.addDevice("10.0.0.9", "Unpinged")
	pm.sortBy(sortLoss)
	if pm.devices[0].Name != "Unpinged" || pm.devices[2].Fail != 5 {
		t.Errorf("loss sort asc = %v", []string{pm.devices[0].Name, pm.devices[1].Name, pm.devices[2].Name})
	}
	pm.sortBy(sortLoss)
	if pm.devices[0].Fail != 5 || pm.devices[2].Name != "Unpinged" {
		t.Errorf("loss sort desc = %v", []string{pm.devices[0].Name, pm.devices[1].Name, pm.devices[2].Name})
	}
	pm.removeDevice(2)
	pm.clearStats()

	// exercise the resizer drag path (custom renderer + layout)
	r := newColResizer(func(dx float32) { pm.colWidths[0] += dx })
	_ = test.WidgetRenderer(r)
	r.Refresh()
	before := pm.colWidths[0]
	r.Dragged(&fyne.DragEvent{Dragged: fyne.NewDelta(25, 0)})
	r.DragEnd()
	if pm.colWidths[0] != before+25 {
		t.Errorf("drag width = %v, want %v", pm.colWidths[0], before+25)
	}

	pm.removeDevice(0)
	if len(pm.devices) != 1 {
		t.Errorf("after remove, len = %d", len(pm.devices))
	}
	pm.clearStats()
}

func TestStatusLine(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()

	win := a.NewWindow("PMS")
	pm := newAppState(win, fyne.NewStaticResource("trash.png", trashPNG))
	win.SetContent(pm.buildUI(fyne.NewStaticResource("ping-pong.png", pingPongPNG)))

	if got := pm.statusLabel.Text; got != "No devices" {
		t.Errorf("empty status = %q", got)
	}

	pm.addDevice("10.0.0.1", "a")
	if got := pm.statusLabel.Text; got != "Stopped  ·  1 device" {
		t.Errorf("stopped status = %q", got)
	}

	pm.addDevice("10.0.0.2", "b")
	pm.addDevice("10.0.0.3", "c")
	pm.running = true
	pm.devices[0].Total, pm.devices[0].LastResult = 3, true
	pm.devices[1].Total, pm.devices[1].LastResult = 3, false
	// devices[2] never pinged -> pending, not "down"
	pm.refreshStatus()
	want := "Monitoring  ·  3 devices  ·  1 up  ·  1 down  ·  1 pending"
	if got := pm.statusLabel.Text; got != want {
		t.Errorf("running status = %q, want %q", got, want)
	}
}
