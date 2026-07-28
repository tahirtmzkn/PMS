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
	resizerWidth   float32 = 6
	minColumnWidth float32 = 50
	removeColWidth float32 = 40
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

// colResizer is a thin draggable divider that widens/narrows the column to
// its left when dragged horizontally.
type colResizer struct {
	widget.BaseWidget
	line   *canvas.Rectangle
	onDrag func(dx float32)
}

func newColResizer(onDrag func(dx float32)) *colResizer {
	r := &colResizer{line: canvas.NewRectangle(theme.Color(theme.ColorNameSeparator)), onDrag: onDrag}
	r.ExtendBaseWidget(r)
	return r
}

func (r *colResizer) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(r.line)
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
