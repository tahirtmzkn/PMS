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
	// device is what ties a row to its data. Rows are looked up by this
	// pointer rather than by list index, so reordering the list (drag) or
	// removing a device can't leave a row updating someone else's counters.
	device *Device
	// lifted marks the row as picked up by its grip: drawn above its
	// neighbours with an opaque background so it hides what it passes over.
	lifted   bool
	bg       *canvas.Rectangle
	name     *widget.Label
	hostname *widget.Label
	success  *widget.Label
	fail     *widget.Label
	total    *widget.Label
	loss     *widget.Label
	content  fyne.CanvasObject
}

const (
	// unknownName is the Name column's stand-in for a device added with the name
	// box left blank. emptyHostname is what the Hostname column shows when
	// nothing answered the SNMP query, and resolvingHostname the placeholder
	// while it's in flight.
	unknownName       = "Unknown"
	emptyHostname     = "Empty"
	resolvingHostname = "Resolving…"
)

// sortColumn identifies which column the table is currently sorted by.
type sortColumn int

const (
	sortNone sortColumn = iota
	sortName
	sortHostname
	sortIP
	sortSuccess
	sortFail
	sortTotal
	sortLoss
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

	// colWidths holds the current width of the Name/Hostname/IP/Success/Fail/
	// Total/Loss columns, user-adjustable by dragging the header resizers.
	colWidths []float32

	// controlHeight is the shared height of every entry/button/select.
	controlHeight float32

	sortCol sortColumn
	sortAsc bool

	// lookupName resolves a device's name from its IP (SNMP sysName). A field
	// rather than a direct call so tests can stub it instead of shelling out.
	lookupName func(ip, iface string) string

	// configFile is the JSON file the device list is saved to and restored from
	// (see config.go). Empty means no persistence, which is what newAppState
	// leaves it as: main.go points it at the real config file, so a test that
	// doesn't ask for persistence cannot write over the user's own list.
	configFile string

	// dragDevice is the device whose grip is currently being dragged, and
	// dragOffset the vertical travel accumulated since its last row swap.
	dragDevice *Device
	dragOffset float32

	// rowOffsets is the extra Y each row is currently drawn at, keyed by row
	// content object because that is what rowsLayout sees. Non-zero only while
	// a row is being dragged or is animating back into its slot; rowAnims
	// holds those animations so a new drag can cut one short.
	rowOffsets map[fyne.CanvasObject]float32
	rowAnims   map[fyne.CanvasObject]*fyne.Animation

	toggleBtn      *widget.Button
	statusLabel    *widget.Label
	ifaceSelect    *widget.Select
	nameHeaderBtn  *widget.Button
	hostHeaderBtn  *widget.Button
	ipHeaderBtn    *widget.Button
	successHeader  *widget.Button
	failHeaderBtn  *widget.Button
	totalHeaderBtn *widget.Button
	lossHeaderBtn  *widget.Button
}

func newAppState(win fyne.Window, trashIcon fyne.Resource) *appState {
	pm := &appState{
		win:           win,
		trashIcon:     trashIcon,
		pingInterval:  1,
		pingTimeout:   1000,
		interfaceName: "enp3s0",
		// Seven columns have to fit the default 1200px window alongside the
		// dividers and the two trailing button cells, or the row overflows and
		// the grip/remove buttons land off-screen; a drag widens any of them.
		colWidths:  []float32{170, 200, 140, 110, 110, 110, 110},
		rowOffsets: map[fyne.CanvasObject]float32{},
		rowAnims:   map[fyne.CanvasObject]*fyne.Animation{},
		lookupName: snmpSysName,
	}
	pm.rowsContainer = container.New(rowsLayout{
		slots:  pm.slotObjects,
		offset: func(o fyne.CanvasObject) float32 { return pm.rowOffsets[o] },
	})
	return pm
}

// slotObjects lists the row contents in list order — the order they occupy
// slots in, as opposed to the order they are painted in (see syncPaintOrder).
func (pm *appState) slotObjects() []fyne.CanvasObject {
	objs := make([]fyne.CanvasObject, len(pm.rows))
	for i, row := range pm.rows {
		objs[i] = row.content
	}
	return objs
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
	nameEntry.SetPlaceHolder("Name (optional)")

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
	pm.hostHeaderBtn = pm.newHeaderButton("Hostname", sortHostname)
	pm.ipHeaderBtn = pm.newHeaderButton("IP", sortIP)
	pm.successHeader = pm.newHeaderButton("Success", sortSuccess)
	pm.failHeaderBtn = pm.newHeaderButton("Fail", sortFail)
	pm.totalHeaderBtn = pm.newHeaderButton("Total", sortTotal)
	pm.lossHeaderBtn = pm.newHeaderButton("Loss", sortLoss)

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
		pm.columnCell(1, pm.hostHeaderBtn), newResizer(1),
		pm.columnCell(2, pm.ipHeaderBtn), newResizer(2),
		pm.columnCell(3, pm.successHeader), newResizer(3),
		pm.columnCell(4, pm.failHeaderBtn), newResizer(4),
		pm.columnCell(5, pm.totalHeaderBtn), newResizer(5),
		pm.columnCell(6, pm.lossHeaderBtn), newResizer(6),
		layout.NewSpacer(),
		// Two blanks: one per trailing button cell (grip, remove), so the
		// header lines up with the rows.
		fixedWidth(removeColWidth, widget.NewLabel("")),
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

// addDevice appends a device to the table. The Name column is what the user
// typed, or "Unknown" when the box was left blank — it is never filled in from
// SNMP. The Hostname column always is: the row goes up with a placeholder there
// and the lookup fills it in when it answers. The returned channel closes once
// that lookup's result has reached the UI (nil if no device was added) — the app
// ignores it, tests wait on it.
func (pm *appState) addDevice(ip, name string) <-chan struct{} {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		dialog.ShowError(errors.New("please enter an IP address"), pm.win)
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = unknownName
	}

	device := &Device{IP: ip, Name: name, Hostname: resolvingHostname}
	pm.devices = append(pm.devices, device)
	pm.refreshRows()
	pm.persistDevices()

	return pm.startHostnameLookup(device)
}

// startHostnameLookup asks a device for its SNMP sysName. The query is a
// subprocess, so it runs on its own goroutine and comes back through fyne.Do;
// the device is carried by pointer, so sorting, dragging or removing it while
// the query is in flight is harmless.
func (pm *appState) startHostnameLookup(device *Device) <-chan struct{} {
	// Everything the goroutine needs is read here, on the UI goroutine.
	lookup, ip, iface := pm.lookupName, device.IP, pm.interfaceName

	done := make(chan struct{})
	go func() {
		defer close(done)
		host := lookup(ip, iface)
		fyne.Do(func() { pm.applyResolvedHostname(device, host) })
	}()
	return done
}

// applyResolvedHostname writes a looked-up sysName onto a device and its row,
// falling back to "Empty" when nothing answered.
func (pm *appState) applyResolvedHostname(device *Device, host string) {
	if pm.indexOf(device) < 0 {
		return // removed while the query was in flight
	}
	if host == "" {
		host = emptyHostname
	}
	device.Hostname = host
	if row := pm.rowFor(device); row != nil {
		setLabel(row.hostname, host)
	}
}

func (pm *appState) removeDevice(idx int) {
	if idx < 0 || idx >= len(pm.devices) {
		return
	}
	pm.devices = append(pm.devices[:idx], pm.devices[idx+1:]...)
	pm.refreshRows()
	pm.persistDevices()
}

func (pm *appState) clearStats() {
	for _, d := range pm.devices {
		d.Success, d.Fail, d.Total = 0, 0, 0
	}
	pm.refreshRows()
}

// refreshRows fully rebuilds every row. Called after any structural change
// (add/remove/clear/stop/sort) — mirrors the Python version's full table
// refresh. Not usable mid-drag: see swapRows.
func (pm *appState) refreshRows() {
	// The rows these animations are moving are about to be thrown away, and
	// their map entries with them.
	for _, anim := range pm.rowAnims {
		anim.Stop()
	}
	clear(pm.rowAnims)
	clear(pm.rowOffsets)

	pm.rows = make([]*deviceRow, len(pm.devices))
	for idx, device := range pm.devices {
		pm.rows[idx] = pm.newRow(device)
	}
	pm.syncPaintOrder()
	pm.refreshStatus()
}

// syncPaintOrder rebuilds the container's Objects from the current row order.
// Paint order is slot order, except that a lifted row goes last so it draws
// above the rows it is passing over — rowsLayout takes slot order from
// pm.rows, not from Objects, so this doesn't move any row's slot.
func (pm *appState) syncPaintOrder() {
	objs := make([]fyne.CanvasObject, 0, len(pm.rows))
	var lifted fyne.CanvasObject
	for _, row := range pm.rows {
		if row.lifted {
			lifted = row.content
			continue
		}
		objs = append(objs, row.content)
	}
	if lifted != nil {
		objs = append(objs, lifted)
	}
	pm.rowsContainer.Objects = objs
	pm.rowsContainer.Refresh()
}

// layoutRows re-positions the rows without Container.Refresh's deep refresh of
// every child widget: an animation frame only moves rows, it doesn't change any
// text or color.
func (pm *appState) layoutRows() {
	if pm.rowsContainer.Layout == nil {
		return
	}
	pm.rowsContainer.Layout.Layout(pm.rowsContainer.Objects, pm.rowsContainer.Size())
	canvas.Refresh(pm.rowsContainer)
}

func (pm *appState) newRow(device *Device) *deviceRow {
	bg := canvas.NewRectangle(color.Transparent)
	bg.CornerRadius = 6

	nameLbl := widget.NewLabel(device.Name)
	hostLbl := widget.NewLabel(device.Hostname)
	ipLbl := widget.NewLabel(device.IP)
	successLbl := widget.NewLabel(strconv.Itoa(device.Success))
	failLbl := widget.NewLabel(strconv.Itoa(device.Fail))
	totalLbl := widget.NewLabel(strconv.Itoa(device.Total))
	lossLbl := widget.NewLabel(formatLoss(device.Fail, device.Total))

	removeBtn := widget.NewButtonWithIcon("", pm.trashIcon, func() {
		pm.removeDevice(pm.indexOf(device))
	})
	removeBtn.Importance = widget.LowImportance

	handle := newDragHandle(
		func(dy float32) { pm.dragRow(device, dy) },
		pm.endRowDrag,
	)

	grid := container.NewHBox(
		pm.columnCell(0, nameLbl), blankGap(resizerWidth),
		pm.columnCell(1, hostLbl), blankGap(resizerWidth),
		pm.columnCell(2, ipLbl), blankGap(resizerWidth),
		pm.columnCell(3, successLbl), blankGap(resizerWidth),
		pm.columnCell(4, failLbl), blankGap(resizerWidth),
		pm.columnCell(5, totalLbl), blankGap(resizerWidth),
		pm.columnCell(6, lossLbl), blankGap(resizerWidth),
		layout.NewSpacer(),
		fixedWidth(removeColWidth, handle),
		fixedWidth(removeColWidth, removeBtn),
	)

	row := &deviceRow{device: device, bg: bg, name: nameLbl, hostname: hostLbl, success: successLbl, fail: failLbl, total: totalLbl, loss: lossLbl}
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

	// A lifted row is drawn over the rows it passes, so its tint is composited
	// onto the window background rather than left translucent — otherwise the
	// row underneath shows straight through it — and a hairline outline gives
	// it an edge even against a neighbour of the same status color.
	stroke := float32(0)
	strokeCol := color.Color(color.Transparent)
	if row.lifted {
		col = opaqueOver(col, theme.Color(theme.ColorNameBackground))
		strokeCol = theme.Color(theme.ColorNameSeparator)
		stroke = 1
	}

	// A device that stays up (or stays down) keeps the same colors cycle after
	// cycle, and Refresh is a canvas repaint request — skip the no-op.
	if row.bg.FillColor == col && row.bg.StrokeWidth == stroke {
		return
	}
	row.bg.FillColor = col
	row.bg.StrokeColor = strokeCol
	row.bg.StrokeWidth = stroke
	row.bg.Refresh()
}

// opaqueOver composites the (possibly translucent) fg over an opaque bg and
// returns the result at full alpha. Color.RGBA values are alpha-premultiplied,
// which is why fg needs no extra scaling here.
func opaqueOver(fg, bg color.Color) color.NRGBA {
	fr, fgr, fb, fa := fg.RGBA()
	br, bgr, bb, _ := bg.RGBA()
	inv := 1 - float64(fa)/0xffff
	mix := func(f, b uint32) uint8 {
		return uint8((float64(f) + float64(b)*inv) / 0x101)
	}
	return color.NRGBA{R: mix(fr, br), G: mix(fgr, bgr), B: mix(fb, bb), A: 0xff}
}

// indexOf returns device's current position in pm.devices, or -1 if it's gone.
// Row callbacks resolve their index through this instead of capturing it, so a
// reorder or removal can't leave a button aimed at the wrong device.
func (pm *appState) indexOf(device *Device) int {
	for i, d := range pm.devices {
		if d == device {
			return i
		}
	}
	return -1
}

// rowFor finds the row currently showing device, or nil if it has no row (it
// was removed while a ping cycle was still in flight for it).
func (pm *appState) rowFor(device *Device) *deviceRow {
	for _, row := range pm.rows {
		if row.device == device {
			return row
		}
	}
	return nil
}

// dragRow reorders the device list while a row's grip is dragged. The row is
// drawn at its accumulated travel so it follows the pointer, and once that
// travel covers a whole row the device swaps with its neighbour — at which
// point the travel drops by the same stride, so the row's position on screen
// stays continuous across the swap. Travel is zeroed at either end of the list
// so overshooting past the last row doesn't have to be unwound before the row
// responds to being dragged back.
func (pm *appState) dragRow(device *Device, dy float32) {
	row := pm.rowFor(device)
	if row == nil {
		return
	}
	if pm.dragDevice != device {
		pm.endRowDrag()
		pm.dragDevice = device
		pm.dragOffset = 0
		pm.liftRow(row)
	}

	stride := pm.rowStride()
	if stride <= 0 {
		return
	}

	pm.dragOffset += dy
	for pm.dragOffset >= stride {
		if !pm.swapRows(pm.indexOf(device), 1) {
			pm.dragOffset = 0
			break
		}
		pm.dragOffset -= stride
	}
	for pm.dragOffset <= -stride {
		if !pm.swapRows(pm.indexOf(device), -1) {
			pm.dragOffset = 0
			break
		}
		pm.dragOffset += stride
	}

	pm.rowOffsets[row.content] = pm.dragOffset
	pm.layoutRows()
}

// endRowDrag releases the grip: the row eases from wherever the pointer left
// it into its slot, and stays lifted until it lands so it doesn't spend the
// animation half-overlapping a neighbour.
func (pm *appState) endRowDrag() {
	device, offset := pm.dragDevice, pm.dragOffset
	pm.dragDevice = nil
	pm.dragOffset = 0
	if device == nil {
		return
	}
	if row := pm.rowFor(device); row != nil {
		pm.animateRowOffset(row, offset)
	}
	// Saved here rather than in swapRows, so one drag across the table is one
	// write instead of one per row it passed.
	pm.persistDevices()
}

// liftRow picks a row up: opaque background, thin outline, painted last.
func (pm *appState) liftRow(row *deviceRow) {
	pm.stopRowAnim(row)
	row.lifted = true
	pm.colorRow(row, row.device)
	pm.syncPaintOrder()
}

// animateRowOffset eases a row from a visual offset back into its slot — the
// row displaced by a swap sliding into the vacated slot, or the dragged row
// settling once released. The offset is applied up front so the first frame
// doesn't flash the row at its destination.
func (pm *appState) animateRowOffset(row *deviceRow, from float32) {
	pm.stopRowAnim(row)
	if from == 0 || !fyne.CurrentApp().Settings().ShowAnimations() {
		pm.settleRow(row)
		return
	}

	obj := row.content
	pm.rowOffsets[obj] = from
	anim := pm.rowSlideAnimation(row, from)
	pm.rowAnims[obj] = anim
	anim.Start()
}

// rowSlideAnimation builds, but does not start, the animation that walks a
// row's offset back down to zero. Split out from animateRowOffset so the
// interpolation can be driven a frame at a time in tests — Fyne's test driver
// completes any animation it is handed instantly.
func (pm *appState) rowSlideAnimation(row *deviceRow, from float32) *fyne.Animation {
	obj := row.content
	anim := fyne.NewAnimation(canvas.DurationShort, func(p float32) {
		if p >= 1 {
			delete(pm.rowAnims, obj)
			pm.settleRow(row)
			return
		}
		pm.rowOffsets[obj] = from * (1 - p)
		pm.layoutRows()
	})
	anim.Curve = fyne.AnimationEaseOut
	return anim
}

// settleRow drops a row back into its slot: no offset, no lift.
func (pm *appState) settleRow(row *deviceRow) {
	delete(pm.rowOffsets, row.content)
	if row.lifted {
		row.lifted = false
		pm.colorRow(row, row.device)
		pm.syncPaintOrder()
		return
	}
	pm.layoutRows()
}

func (pm *appState) stopRowAnim(row *deviceRow) {
	if anim := pm.rowAnims[row.content]; anim != nil {
		anim.Stop()
		delete(pm.rowAnims, row.content)
	}
}

// rowStride is the on-screen distance from one row to the next: the row's own
// height plus the VBox padding between them. Falls back to MinSize before the
// first layout has run.
func (pm *appState) rowStride() float32 {
	if len(pm.rows) == 0 {
		return 0
	}
	h := pm.rows[0].content.Size().Height
	if h <= 0 {
		h = pm.rows[0].content.MinSize().Height
	}
	return h + theme.Padding()
}

// swapRows moves the device at idx one place in direction delta, swapping the
// existing row widgets instead of calling refreshRows: a rebuild mid-drag
// would detach the very grip the driver is delivering drag events to. The
// displaced row's slot jumps a whole stride, so it starts where it was and
// slides into the vacated one. A manual reorder also invalidates whatever
// order a header arrow is claiming, so the sort indicator is dropped.
func (pm *appState) swapRows(idx, delta int) bool {
	to := idx + delta
	if idx < 0 || idx >= len(pm.devices) || to < 0 || to >= len(pm.devices) {
		return false
	}

	displaced := pm.rows[to]
	stride := pm.rowStride()

	pm.devices[idx], pm.devices[to] = pm.devices[to], pm.devices[idx]
	pm.rows[idx], pm.rows[to] = pm.rows[to], pm.rows[idx]
	pm.syncPaintOrder()
	pm.animateRowOffset(displaced, float32(delta)*stride)

	if pm.sortCol != sortNone {
		pm.sortCol = sortNone
		pm.refreshHeaderLabels()
	}
	return true
}

// updateRowResult is the cheap per-cycle path: only the counters and
// background color of one existing row change, no rebuild.
func (pm *appState) updateRowResult(device *Device) {
	row := pm.rowFor(device)
	if row == nil {
		return
	}
	setLabel(row.success, strconv.Itoa(device.Success))
	setLabel(row.fail, strconv.Itoa(device.Fail))
	setLabel(row.total, strconv.Itoa(device.Total))
	setLabel(row.loss, formatLoss(device.Fail, device.Total))
	pm.colorRow(row, device)
}

// setLabel skips Label.SetText's unconditional Refresh when the text hasn't
// actually changed. Per cycle only one of Success/Fail moves and Loss usually
// holds steady, so on a long list this drops a good share of the per-cycle
// repaint requests.
func setLabel(l *widget.Label, text string) {
	if l.Text == text {
		return
	}
	l.SetText(text)
}

// formatLoss shows failed pings as a share of the total, e.g. "%12.5". One
// decimal is kept (a trailing ".0" is dropped) so a single failure in a long
// run doesn't round away to "%0". A device with no pings yet shows "-" rather
// than "%0", which would claim a clean link before anything was measured.
func formatLoss(fail, total int) string {
	if total == 0 {
		return "-"
	}
	pct := math.Round(float64(fail)/float64(total)*1000) / 10
	return "%" + strconv.FormatFloat(pct, 'f', -1, 64)
}

// lossRatio is the sort key for the Loss column. Never-pinged devices get -1
// so they group together ahead of every measured device instead of tying with
// the 0%-loss ones.
func lossRatio(d *Device) float64 {
	if d.Total == 0 {
		return -1
	}
	return float64(d.Fail) / float64(d.Total)
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
		case sortHostname:
			lt = strings.ToLower(a.Hostname) < strings.ToLower(b.Hostname)
		case sortIP:
			lt = ipLess(a.IP, b.IP)
		case sortSuccess:
			lt = a.Success < b.Success
		case sortFail:
			lt = a.Fail < b.Fail
		case sortTotal:
			lt = a.Total < b.Total
		case sortLoss:
			lt = lossRatio(a) < lossRatio(b)
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
	// The saved list is an ordered one, and a sort is what the user now wants
	// that order to be.
	pm.persistDevices()
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
	set(pm.hostHeaderBtn, "Hostname", sortHostname)
	set(pm.ipHeaderBtn, "IP", sortIP)
	set(pm.successHeader, "Success", sortSuccess)
	set(pm.failHeaderBtn, "Fail", sortFail)
	set(pm.totalHeaderBtn, "Total", sortTotal)
	set(pm.lossHeaderBtn, "Loss", sortLoss)
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
		func(d *Device) {
			fyne.Do(func() { pm.updateRowResult(d) })
		},
		func() {
			fyne.Do(func() {
				pm.isPinging = false
				pm.refreshStatus()
			})
		},
	)
}
