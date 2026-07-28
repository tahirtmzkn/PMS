package main

import (
	"bytes"
	"errors"
	"fmt"
	"image/color"
	"math"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// tinted returns c with alpha a, used to derive row-highlight colors from the
// same theme colors the Start/Stop button already uses — one source of truth
// instead of separate hardcoded hues, and it stays correct across themes.
func tinted(c color.Color, a uint8) color.Color {
	r, g, b, _ := c.RGBA()
	return color.NRGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: a}
}

// deviceRow holds the live widgets for one table row so per-cycle updates can
// touch just the text/color instead of rebuilding the row.
type deviceRow struct {
	bg      *canvas.Rectangle
	success *widget.Label
	fail    *widget.Label
	total   *widget.Label
	content fyne.CanvasObject
}

// sortColumn identifies which column the table is currently sorted by.
type sortColumn int

const (
	sortNone sortColumn = iota
	sortName
	sortIP
	sortSuccess
	sortFail
	sortTotal
)

type appState struct {
	win       fyne.Window
	trashIcon fyne.Resource

	devices []*Device

	pingInterval  int // seconds
	pingTimeout   int // ms
	interfaceName string

	running    bool
	isPinging  bool
	tickerStop chan struct{}

	rows          []*deviceRow
	rowsContainer *fyne.Container

	// colWidths holds the current width of the Name/IP/Success/Fail/Total
	// columns, user-adjustable by dragging the header resizers.
	colWidths []float32

	// controlHeight is the shared height of every entry/button/select.
	controlHeight float32

	sortCol sortColumn
	sortAsc bool

	toggleBtn      *widget.Button
	statusLabel    *widget.Label
	ifaceSelect    *widget.Select
	nameHeaderBtn  *widget.Button
	ipHeaderBtn    *widget.Button
	successHeader  *widget.Button
	failHeaderBtn  *widget.Button
	totalHeaderBtn *widget.Button
}

func newAppState(win fyne.Window, trashIcon fyne.Resource) *appState {
	return &appState{
		win:           win,
		trashIcon:     trashIcon,
		pingInterval:  1,
		pingTimeout:   1000,
		interfaceName: "enp3s0",
		rowsContainer: container.NewVBox(),
		colWidths:     []float32{220, 160, 140, 100, 100},
	}
}

// sized pins obj to an exact width and height. Box layouts otherwise size
// children to their own tight MinSize, which is why entries (taller) and
// buttons (shorter) render at different heights unless given a common one.
func sized(w, h float32, obj fyne.CanvasObject) fyne.CanvasObject {
	return container.NewGridWrap(fyne.NewSize(w, h), obj)
}

// fixedWidth pins obj to width w, keeping its natural minimum height.
func fixedWidth(w float32, obj fyne.CanvasObject) fyne.CanvasObject {
	return sized(w, obj.MinSize().Height, obj)
}

// maxMinHeight is the tallest MinSize height among objs — used to pick one
// control height that fits every widget type in a toolbar row.
func maxMinHeight(objs ...fyne.CanvasObject) float32 {
	h := float32(0)
	for _, o := range objs {
		if mh := o.MinSize().Height; mh > h {
			h = mh
		}
	}
	return h
}

func (pm *appState) buildUI(pingPongIcon fyne.Resource) fyne.CanvasObject {
	icon := canvas.NewImageFromResource(pingPongIcon)
	icon.FillMode = canvas.ImageFillContain

	ipEntry := widget.NewEntry()
	ipEntry.SetPlaceHolder("IP Address")
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

	const btnWidth = 100

	pm.toggleBtn = widget.NewButton("Start", pm.toggleStartStop)
	pm.toggleBtn.Importance = widget.SuccessImportance

	clearBtn := widget.NewButton("Clear", pm.clearStats)
	clearBtn.Importance = widget.WarningImportance

	// One control height for every entry/button/select in the app, taken from
	// the tallest of the three widget types so none of them get clipped.
	pm.controlHeight = maxMinHeight(ipEntry, addBtn, widget.NewSelect([]string{"wlp0s20f3"}, nil))
	icon.SetMinSize(fyne.NewSize(pm.controlHeight, pm.controlHeight))

	// Single-row toolbar: logo, add-device controls, then Add/Start-Stop/
	// Clear grouped together at the same width, with the rest of the row
	// left empty rather than the buttons being split apart by a spacer.
	topBar := container.NewHBox(
		icon,
		sized(200, pm.controlHeight, ipEntry),
		sized(200, pm.controlHeight, nameEntry),
		sized(btnWidth, pm.controlHeight, addBtn),
		sized(btnWidth, pm.controlHeight, pm.toggleBtn),
		sized(btnWidth, pm.controlHeight, clearBtn),
	)

	settingsRow := pm.buildSettingsPanel()

	pm.nameHeaderBtn = pm.newHeaderButton("Name", sortName)
	pm.ipHeaderBtn = pm.newHeaderButton("IP", sortIP)
	pm.successHeader = pm.newHeaderButton("Success", sortSuccess)
	pm.failHeaderBtn = pm.newHeaderButton("Fail", sortFail)
	pm.totalHeaderBtn = pm.newHeaderButton("Total", sortTotal)

	var headerRow *fyne.Container
	newResizer := func(col int) fyne.CanvasObject {
		return newColResizer(func(dx float32) {
			pm.colWidths[col] += dx
			if pm.colWidths[col] < minColumnWidth {
				pm.colWidths[col] = minColumnWidth
			}
			headerRow.Refresh()
			pm.rowsContainer.Refresh()
		})
	}

	headerRow = container.NewHBox(
		pm.columnCell(0, pm.nameHeaderBtn), newResizer(0),
		pm.columnCell(1, pm.ipHeaderBtn), newResizer(1),
		pm.columnCell(2, pm.successHeader), newResizer(2),
		pm.columnCell(3, pm.failHeaderBtn), newResizer(3),
		pm.columnCell(4, pm.totalHeaderBtn), newResizer(4),
		layout.NewSpacer(),
		fixedWidth(removeColWidth, widget.NewLabel("")),
	)

	// A tinted band behind the header row so the column names read as a
	// header strip, with the resizer dividers separating them.
	header := container.NewStack(newThemedRect(theme.ColorNameInputBackground), headerRow)

	top := container.NewVBox(
		container.NewPadded(topBar),
		widget.NewSeparator(),
		container.NewPadded(settingsRow),
		widget.NewSeparator(),
		header,
	)

	pm.statusLabel = widget.NewLabel("")
	pm.refreshStatus()
	statusBar := container.NewVBox(
		widget.NewSeparator(),
		container.NewPadded(pm.statusLabel),
	)

	scroll := container.NewVScroll(pm.rowsContainer)

	return container.NewBorder(top, statusBar, nil, nil, scroll)
}

// refreshStatus rewrites the bottom status line. "pending" covers devices
// added (or just cleared) that this cycle hasn't reached yet, so they aren't
// misreported as down before their first ping.
func (pm *appState) refreshStatus() {
	if pm.statusLabel == nil {
		return
	}

	n := len(pm.devices)
	if n == 0 {
		pm.statusLabel.SetText("No devices")
		return
	}

	// While stopped the table shows no up/down coloring either, so the
	// device count is all that's meaningful.
	if !pm.running {
		pm.statusLabel.SetText("Stopped  ·  " + plural(n, "device"))
		return
	}

	up, down, pending := 0, 0, 0
	for _, d := range pm.devices {
		switch {
		case d.Total == 0:
			pending++
		case d.LastResult:
			up++
		default:
			down++
		}
	}

	parts := []string{"Monitoring", plural(n, "device")}
	if up > 0 || down > 0 {
		parts = append(parts, fmt.Sprintf("%d up", up), fmt.Sprintf("%d down", down))
	}
	if pending > 0 {
		parts = append(parts, fmt.Sprintf("%d pending", pending))
	}
	pm.statusLabel.SetText(strings.Join(parts, "  ·  "))
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func (pm *appState) addDevice(ip, name string) {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		dialog.ShowError(errors.New("please enter an IP address"), pm.win)
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Unknown"
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
	pm.refreshStatus()
}

func (pm *appState) newRow(idx int, device *Device) *deviceRow {
	bg := canvas.NewRectangle(color.Transparent)
	bg.CornerRadius = 6

	nameLbl := widget.NewLabel(device.Name)
	ipLbl := widget.NewLabel(device.IP)
	successLbl := widget.NewLabel(formatSuccess(device.Success, device.Total))
	failLbl := widget.NewLabel(strconv.Itoa(device.Fail))
	totalLbl := widget.NewLabel(strconv.Itoa(device.Total))

	removeBtn := widget.NewButtonWithIcon("", pm.trashIcon, func() {
		pm.removeDevice(idx)
	})
	removeBtn.Importance = widget.LowImportance

	grid := container.NewHBox(
		pm.columnCell(0, nameLbl), blankGap(resizerWidth),
		pm.columnCell(1, ipLbl), blankGap(resizerWidth),
		pm.columnCell(2, successLbl), blankGap(resizerWidth),
		pm.columnCell(3, failLbl), blankGap(resizerWidth),
		pm.columnCell(4, totalLbl), blankGap(resizerWidth),
		layout.NewSpacer(),
		fixedWidth(removeColWidth, removeBtn),
	)

	row := &deviceRow{bg: bg, success: successLbl, fail: failLbl, total: totalLbl}
	row.content = container.NewStack(bg, grid)
	pm.colorRow(row, device)
	return row
}

func (pm *appState) colorRow(row *deviceRow, device *Device) {
	col := color.Color(color.Transparent)
	if pm.running {
		if device.LastResult {
			col = tinted(theme.SuccessColor(), 90)
		} else {
			col = tinted(theme.ErrorColor(), 90)
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
	row.success.SetText(formatSuccess(device.Success, device.Total))
	row.fail.SetText(strconv.Itoa(device.Fail))
	row.total.SetText(strconv.Itoa(device.Total))
	pm.colorRow(row, device)
}

// formatSuccess shows the success count alongside its percentage of total
// pings, e.g. "10 (%50)"; just the count while there's no data yet.
func formatSuccess(success, total int) string {
	if total == 0 {
		return strconv.Itoa(success)
	}
	pct := int(math.Round(float64(success) / float64(total) * 100))
	return fmt.Sprintf("%d (%%%d)", success, pct)
}

// newHeaderButton makes a column header that sorts the table by col when
// clicked, toggling ascending/descending on repeated clicks.
func (pm *appState) newHeaderButton(label string, col sortColumn) *widget.Button {
	btn := widget.NewButton(label, func() { pm.sortBy(col) })
	btn.Importance = widget.LowImportance
	btn.Alignment = widget.ButtonAlignLeading
	return btn
}

// sortBy sorts the device list by col, toggling direction if col is already
// the active sort column, then re-renders the header arrows and table.
func (pm *appState) sortBy(col sortColumn) {
	if pm.sortCol == col {
		pm.sortAsc = !pm.sortAsc
	} else {
		pm.sortCol = col
		pm.sortAsc = true
	}

	less := func(i, j int) bool {
		a, b := pm.devices[i], pm.devices[j]
		var lt bool
		switch col {
		case sortName:
			lt = strings.ToLower(a.Name) < strings.ToLower(b.Name)
		case sortIP:
			lt = ipLess(a.IP, b.IP)
		case sortSuccess:
			lt = a.Success < b.Success
		case sortFail:
			lt = a.Fail < b.Fail
		case sortTotal:
			lt = a.Total < b.Total
		}
		return lt
	}
	if pm.sortAsc {
		sort.SliceStable(pm.devices, less)
	} else {
		sort.SliceStable(pm.devices, func(i, j int) bool { return less(j, i) })
	}

	pm.refreshHeaderLabels()
	pm.refreshRows()
}

func (pm *appState) refreshHeaderLabels() {
	set := func(btn *widget.Button, label string, col sortColumn) {
		if pm.sortCol != col {
			btn.SetText(label)
			return
		}
		if pm.sortAsc {
			btn.SetText(label + " ▲")
		} else {
			btn.SetText(label + " ▼")
		}
	}
	set(pm.nameHeaderBtn, "Name", sortName)
	set(pm.ipHeaderBtn, "IP", sortIP)
	set(pm.successHeader, "Success", sortSuccess)
	set(pm.failHeaderBtn, "Fail", sortFail)
	set(pm.totalHeaderBtn, "Total", sortTotal)
}

// ipLess orders dotted-quad IPs numerically (so 9.0.0.1 sorts before
// 10.0.0.1); falls back to a plain string compare for unparseable input.
func ipLess(a, b string) bool {
	ipA, ipB := net.ParseIP(a), net.ParseIP(b)
	if ipA != nil && ipB != nil {
		return bytes.Compare(ipA.To16(), ipB.To16()) < 0
	}
	return a < b
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
	pm.refreshStatus()
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
			fyne.Do(func() {
				pm.isPinging = false
				pm.refreshStatus()
			})
		},
	)
}
