package main

import (
	"errors"
	"fmt"
	"image/color"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

var (
	successColor = color.NRGBA{R: 76, G: 175, B: 80, A: 90}
	failColor    = color.NRGBA{R: 244, G: 67, B: 54, A: 90}
)

// deviceRow holds the live widgets for one table row so per-cycle updates can
// touch just the text/color instead of rebuilding the row.
type deviceRow struct {
	bg      *canvas.Rectangle
	success *widget.Label
	fail    *widget.Label
	total   *widget.Label
	content fyne.CanvasObject
}

type appState struct {
	win       fyne.Window
	trashIcon fyne.Resource

	devices      []*Device
	unnamedCount int

	pingInterval  int // seconds
	pingTimeout   int // ms
	interfaceName string

	running    bool
	isPinging  bool
	tickerStop chan struct{}

	rows          []*deviceRow
	rowsContainer *fyne.Container

	toggleBtn *widget.Button
}

func newAppState(win fyne.Window, trashIcon fyne.Resource) *appState {
	return &appState{
		win:           win,
		trashIcon:     trashIcon,
		pingInterval:  1,
		pingTimeout:   1000,
		interfaceName: "enp3s0",
		rowsContainer: container.NewVBox(),
	}
}

// fixedWidth pins obj to width w (height stays its natural minimum), since
// Box layouts otherwise size children to their own tight MinSize.
func fixedWidth(w float32, obj fyne.CanvasObject) fyne.CanvasObject {
	return container.NewGridWrap(fyne.NewSize(w, obj.MinSize().Height), obj)
}

func (pm *appState) buildUI(pingPongIcon fyne.Resource) fyne.CanvasObject {
	const controlWidth = 260

	icon := canvas.NewImageFromResource(pingPongIcon)
	icon.FillMode = canvas.ImageFillContain
	icon.SetMinSize(fyne.NewSize(48, 48))

	ipEntry := widget.NewEntry()
	ipEntry.SetPlaceHolder("IP address")
	nameEntry := widget.NewEntry()
	nameEntry.SetPlaceHolder("Device Name (optional)")

	addBtn := widget.NewButton("Add", func() {
		pm.addDevice(ipEntry.Text, nameEntry.Text)
		ipEntry.SetText("")
		nameEntry.SetText("")
	})
	addBtn.Importance = widget.HighImportance
	ipEntry.OnSubmitted = func(string) { addBtn.OnTapped() }
	nameEntry.OnSubmitted = func(string) { addBtn.OnTapped() }

	left := container.NewVBox(
		fixedWidth(controlWidth, ipEntry),
		fixedWidth(controlWidth, nameEntry),
		fixedWidth(controlWidth, addBtn),
	)

	pm.toggleBtn = widget.NewButton("Start", pm.toggleStartStop)
	pm.toggleBtn.Importance = widget.SuccessImportance

	clearBtn := widget.NewButton("Clear", pm.clearStats)
	clearBtn.Importance = widget.WarningImportance

	settingsBtn := widget.NewButton("Settings", pm.openSettings)

	right := container.NewVBox(
		fixedWidth(controlWidth, pm.toggleBtn),
		fixedWidth(controlWidth, clearBtn),
		fixedWidth(controlWidth, settingsBtn),
	)

	topBar := container.NewHBox(icon, left, layout.NewSpacer(), right)

	bold := fyne.TextStyle{Bold: true}
	header := container.NewGridWithColumns(6,
		widget.NewLabelWithStyle("Name", fyne.TextAlignLeading, bold),
		widget.NewLabelWithStyle("IP", fyne.TextAlignLeading, bold),
		widget.NewLabelWithStyle("Success", fyne.TextAlignLeading, bold),
		widget.NewLabelWithStyle("Fail", fyne.TextAlignLeading, bold),
		widget.NewLabelWithStyle("Total", fyne.TextAlignLeading, bold),
		widget.NewLabel(""),
	)

	top := container.NewVBox(topBar, widget.NewSeparator(), header)
	scroll := container.NewVScroll(pm.rowsContainer)

	return container.NewBorder(top, nil, nil, nil, scroll)
}

func (pm *appState) addDevice(ip, name string) {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		dialog.ShowError(errors.New("please enter an IP address"), pm.win)
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		pm.unnamedCount++
		name = fmt.Sprintf("Switch%d", pm.unnamedCount)
	}
	pm.devices = append(pm.devices, &Device{IP: ip, Name: name})
	pm.refreshRows()
}

func (pm *appState) removeDevice(idx int) {
	if idx < 0 || idx >= len(pm.devices) {
		return
	}
	pm.devices = append(pm.devices[:idx], pm.devices[idx+1:]...)
	pm.refreshRows()
}

func (pm *appState) clearStats() {
	for _, d := range pm.devices {
		d.Success, d.Fail, d.Total = 0, 0, 0
	}
	pm.refreshRows()
}

// refreshRows fully rebuilds every row, rebinding each remove button's
// closure to its current index. Called after any structural change
// (add/remove/clear/stop) — mirrors the Python version's full table refresh.
func (pm *appState) refreshRows() {
	pm.rows = make([]*deviceRow, len(pm.devices))
	objects := make([]fyne.CanvasObject, len(pm.devices))
	for idx, device := range pm.devices {
		row := pm.newRow(idx, device)
		pm.rows[idx] = row
		objects[idx] = row.content
	}
	pm.rowsContainer.Objects = objects
	pm.rowsContainer.Refresh()
}

func (pm *appState) newRow(idx int, device *Device) *deviceRow {
	bg := canvas.NewRectangle(color.Transparent)
	bg.CornerRadius = 6

	nameLbl := widget.NewLabel(device.Name)
	ipLbl := widget.NewLabel(device.IP)
	successLbl := widget.NewLabel(strconv.Itoa(device.Success))
	failLbl := widget.NewLabel(strconv.Itoa(device.Fail))
	totalLbl := widget.NewLabel(strconv.Itoa(device.Total))

	removeBtn := widget.NewButtonWithIcon("", pm.trashIcon, func() {
		pm.removeDevice(idx)
	})
	removeBtn.Importance = widget.LowImportance

	grid := container.NewGridWithColumns(6, nameLbl, ipLbl, successLbl, failLbl, totalLbl, removeBtn)

	row := &deviceRow{bg: bg, success: successLbl, fail: failLbl, total: totalLbl}
	row.content = container.NewStack(bg, grid)
	pm.colorRow(row, device)
	return row
}

func (pm *appState) colorRow(row *deviceRow, device *Device) {
	col := color.Color(color.Transparent)
	if pm.running {
		if device.LastResult {
			col = successColor
		} else {
			col = failColor
		}
	}
	row.bg.FillColor = col
	row.bg.Refresh()
}

// updateRowResult is the cheap per-cycle path: only the counters and
// background color of one existing row change, no rebuild.
func (pm *appState) updateRowResult(idx int, device *Device) {
	if idx < 0 || idx >= len(pm.rows) {
		return
	}
	row := pm.rows[idx]
	row.success.SetText(strconv.Itoa(device.Success))
	row.fail.SetText(strconv.Itoa(device.Fail))
	row.total.SetText(strconv.Itoa(device.Total))
	pm.colorRow(row, device)
}

func (pm *appState) toggleStartStop() {
	if pm.running {
		pm.stop()
	} else {
		pm.start()
	}
}

func (pm *appState) start() {
	pm.running = true
	pm.toggleBtn.SetText("Stop")
	pm.toggleBtn.Importance = widget.DangerImportance
	pm.toggleBtn.Refresh()
	pm.startTicker()
}

func (pm *appState) stop() {
	pm.running = false
	pm.stopTicker()
	pm.isPinging = false
	pm.toggleBtn.SetText("Start")
	pm.toggleBtn.Importance = widget.SuccessImportance
	pm.toggleBtn.Refresh()
	pm.refreshRows()
}

// startTicker (re)starts the background ping-cycle timer at pm.pingInterval,
// replacing any previously running one. Safe to call while already running
// (used by Settings to apply a new interval immediately).
func (pm *appState) startTicker() {
	pm.stopTicker()

	stop := make(chan struct{})
	pm.tickerStop = stop
	interval := time.Duration(pm.pingInterval) * time.Second

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				fyne.Do(func() { pm.tick() })
			}
		}
	}()
}

func (pm *appState) stopTicker() {
	if pm.tickerStop != nil {
		close(pm.tickerStop)
		pm.tickerStop = nil
	}
}

// tick runs on the Fyne main goroutine (invoked via fyne.Do from the ticker
// goroutine), so reading/snapshotting appState fields here is race-free. The
// actual ping fan-out happens on a background goroutine so a large device
// list's blocking semaphore never stalls the UI thread.
func (pm *appState) tick() {
	if pm.isPinging || len(pm.devices) == 0 {
		return
	}
	pm.isPinging = true

	devices := pm.devices
	iface := pm.interfaceName
	timeout := pm.pingTimeout

	go runCycle(devices, iface, timeout,
		func(idx int, d *Device) {
			fyne.Do(func() { pm.updateRowResult(idx, d) })
		},
		func() {
			fyne.Do(func() { pm.isPinging = false })
		},
	)
}
