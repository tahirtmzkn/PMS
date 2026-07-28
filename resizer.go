package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	// resizerWidth is the grab area; dividerThickness is the visible line
	// drawn centered inside it, so the handle stays easy to hit without
	// looking like a thick bar.
	resizerWidth     float32 = 7
	dividerThickness float32 = 1
	minColumnWidth   float32 = 50
	removeColWidth   float32 = 40
)

// singleColLayout sizes its one child to a width read live from a closure,
// so dragging a resizer can just call Refresh() on the containers using it
// instead of rebuilding any widgets.
type singleColLayout struct {
	width func() float32
}

func (l singleColLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	if len(objs) == 0 {
		return
	}
	objs[0].Move(fyne.NewPos(0, 0))
	objs[0].Resize(fyne.NewSize(l.width(), size.Height))
}

func (l singleColLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	h := float32(0)
	if len(objs) > 0 {
		h = objs[0].MinSize().Height
	}
	return fyne.NewSize(l.width(), h)
}

// columnCell wraps obj so it always renders at the current width of column i.
func (pm *appState) columnCell(i int, obj fyne.CanvasObject) fyne.CanvasObject {
	return container.New(singleColLayout{width: func() float32 { return pm.colWidths[i] }}, obj)
}

// blankGap is inserted in each data row to line up with a header resizer
// handle, without itself being draggable.
func blankGap(w float32) fyne.CanvasObject {
	return fixedWidth(w, canvas.NewRectangle(color.Transparent))
}

// dragHandle is the per-row reorder grip that sits just left of the remove
// button. It extends widget.Button rather than being a hand-rolled widget so
// its footprint and glyph size are identical to the remove button's by
// construction (both draw their icon at theme.SizeNameInlineIcon), and adds
// the vertical drag reporting a plain Button lacks. No OnTapped is set: a
// click that never crosses the drag threshold is deliberately a no-op.
type dragHandle struct {
	widget.Button
	onDrag    func(dy float32)
	onDragEnd func()
}

func newDragHandle(onDrag func(dy float32), onDragEnd func()) *dragHandle {
	h := &dragHandle{onDrag: onDrag, onDragEnd: onDragEnd}
	h.Icon = theme.MenuIcon()
	h.Importance = widget.LowImportance
	h.ExtendBaseWidget(h)
	return h
}

// Dragged receives the pointer delta since the previous event (not since the
// drag started), which is what makes accumulating it in appState correct.
func (h *dragHandle) Dragged(e *fyne.DragEvent) {
	if h.onDrag != nil {
		h.onDrag(e.Dragged.DY)
	}
}

func (h *dragHandle) DragEnd() {
	if h.onDragEnd != nil {
		h.onDragEnd()
	}
}

func (h *dragHandle) Cursor() desktop.Cursor {
	return desktop.PointerCursor
}

// themedRect is a filled rectangle whose color is resolved from the live
// theme at render time rather than snapshotted at construction. buildUI runs
// before the theme variant has settled, so a plain
// canvas.NewRectangle(theme.Color(...)) there picks up dark-variant colors
// and renders near-black in a light window.
type themedRect struct {
	widget.BaseWidget
	name fyne.ThemeColorName
	rect *canvas.Rectangle
}

func newThemedRect(name fyne.ThemeColorName) *themedRect {
	r := &themedRect{name: name, rect: canvas.NewRectangle(color.Transparent)}
	r.ExtendBaseWidget(r)
	return r
}

func (r *themedRect) CreateRenderer() fyne.WidgetRenderer {
	r.applyColor()
	return widget.NewSimpleRenderer(r.rect)
}

func (r *themedRect) applyColor() {
	r.rect.FillColor = theme.ColorForWidget(r.name, r)
}

func (r *themedRect) Refresh() {
	r.applyColor()
	r.rect.Refresh()
	r.BaseWidget.Refresh()
}

func (r *themedRect) MinSize() fyne.Size {
	return fyne.NewSize(1, 1)
}

// centeredLine draws its single child as a thin full-height vertical line in
// the middle of the available width.
type centeredLine struct{}

func (centeredLine) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	if len(objs) == 0 {
		return
	}
	objs[0].Move(fyne.NewPos((size.Width-dividerThickness)/2, 0))
	objs[0].Resize(fyne.NewSize(dividerThickness, size.Height))
}

func (centeredLine) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(resizerWidth, 1)
}

// colResizer is a thin draggable divider that widens/narrows the column to
// its left when dragged horizontally.
type colResizer struct {
	widget.BaseWidget
	line   *themedRect
	onDrag func(dx float32)
}

func newColResizer(onDrag func(dx float32)) *colResizer {
	r := &colResizer{line: newThemedRect(theme.ColorNameSeparator), onDrag: onDrag}
	r.ExtendBaseWidget(r)
	return r
}

func (r *colResizer) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.New(centeredLine{}, r.line))
}

func (r *colResizer) MinSize() fyne.Size {
	return fyne.NewSize(resizerWidth, 1)
}

func (r *colResizer) Dragged(e *fyne.DragEvent) {
	if r.onDrag != nil {
		r.onDrag(e.Dragged.DX)
	}
}

func (r *colResizer) DragEnd() {}

func (r *colResizer) Cursor() desktop.Cursor {
	return desktop.HResizeCursor
}
