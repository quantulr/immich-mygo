package main

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo/ui"
)

// The image page keeps this much room around the photo at fit, in DIPs, and
// grows it to maxViewerZoom times the fit, a zoomStep a click.
const (
	viewerPad     = 20
	maxViewerZoom = 6
	zoomStep      = 1.25
)

// viewer is the image page: one asset of a timeline shown large, which the
// arrows beside it and the keyboard walk through, and which the wheel,
// pinch and drag zoom and pan.
type viewer struct {
	// feed is the timeline the page browses; id names the asset shown, and
	// dayKey the day the header names.
	feed   *feed
	id     string
	dayKey string
	// bm is the larger image once it loads; err why it did not.
	bm  *ui.Bitmap
	err error
	// zoom is how much larger than fit the photo shows, pan how far its
	// center is from the stage's, in DIPs.
	zoom       float32
	panX, panY float32
	// attempt numbers the loads, so that one that finishes after the page
	// moved to another asset is dropped.
	attempt int
}

// zoomFactor is how much larger than fit the photo shows, 1 before the page
// set a zoom.
func (v *viewer) zoomFactor() float32 {
	if v.zoom < 1 {
		return 1
	}
	return v.zoom
}

// openPhoto shows the image page on a photo of a timeline.
func (a *app) openPhoto(f *feed, day, index int) {
	if day < 0 || day >= len(f.days) {
		return
	}
	d := f.days[day]
	if !d.loaded || index < 0 || index >= len(d.photos) {
		return
	}
	a.main.viewer = &viewer{}
	a.showViewerPhoto(f, d.key, d.photos[index].ID)
}

// showViewerPhoto shows the asset id of the timeline, loading its larger
// image off the UI thread and publishing it through app.update.
func (a *app) showViewerPhoto(f *feed, dayKey, id string) {
	v := a.main.viewer
	if v == nil {
		return
	}
	v.feed = f
	v.dayKey = dayKey
	v.id = id
	v.bm = nil
	v.err = nil
	v.zoom, v.panX, v.panY = 1, 0, 0
	v.attempt++
	attempt := v.attempt
	base, token := a.login.serverURL, a.login.user.Token
	go func() {
		bm, err := fetchPreview(base, token, id)
		a.update(func() {
			if a.main.viewer != v || attempt != v.attempt {
				return
			}
			if err != nil {
				v.err = err
				return
			}
			v.bm = bm
		})
	}()
}

// reloadViewerPhoto loads the image shown again, after it failed.
func (a *app) reloadViewerPhoto() {
	v := a.main.viewer
	if v == nil || v.feed == nil {
		return
	}
	a.showViewerPhoto(v.feed, v.dayKey, v.id)
}

// closeViewer puts the timeline back.
func (a *app) closeViewer() {
	a.main.viewer = nil
}

// viewerSpot is a photo of the loaded timeline, as the page walks it.
type viewerSpot struct {
	dayKey string
	id     string
}

// spots are the photos of the timeline that are loaded, newest first, as
// the page walks them.
func (v *viewer) spots() []viewerSpot {
	if v.feed == nil {
		return nil
	}
	var spots []viewerSpot
	for _, d := range v.feed.days {
		if !d.loaded {
			continue
		}
		for _, p := range d.photos {
			spots = append(spots, viewerSpot{dayKey: d.key, id: p.ID})
		}
	}
	return spots
}

// at is where the photo shown sits among spots, or -1.
func (v *viewer) at(spots []viewerSpot) int {
	for i, s := range spots {
		if s.id == v.id {
			return i
		}
	}
	return -1
}

// viewerNeighbor is the photo delta away in the timeline, if there is one.
func (a *app) viewerNeighbor(delta int) (viewerSpot, bool) {
	v := a.main.viewer
	if v == nil {
		return viewerSpot{}, false
	}
	spots := v.spots()
	cur := v.at(spots)
	if cur < 0 {
		return viewerSpot{}, false
	}
	next := cur + delta
	if next < 0 || next >= len(spots) {
		return viewerSpot{}, false
	}
	return spots[next], true
}

// viewerMove walks the image page delta photos along the timeline.
func (a *app) viewerMove(delta int) {
	s, ok := a.viewerNeighbor(delta)
	if !ok {
		return
	}
	v := a.main.viewer
	a.showViewerPhoto(v.feed, s.dayKey, s.id)
}

// viewerPosition is where the photo shown sits among the loaded ones, and
// how many there are.
func (a *app) viewerPosition() (int, int) {
	v := a.main.viewer
	if v == nil {
		return 0, 0
	}
	spots := v.spots()
	return v.at(spots) + 1, len(spots)
}

// photo is the asset the page shows, or nil once the timeline reloads
// without it.
func (v *viewer) photo() *photo {
	if v.feed == nil {
		return nil
	}
	for _, d := range v.feed.days {
		for _, p := range d.photos {
			if p.ID == v.id {
				return p
			}
		}
	}
	return nil
}

// photoPage is the card's body on the image page: the photo large on a dark
// stage, with the arrows beside it. The keyboard's left and right arrows
// walk the timeline, Escape returns to it, and +, - and 0 zoom. The wheel
// and a pinch zoom about the pointer, a double click toggles between fit
// and twice it, and dragging a zoomed photo pans it.
func (a *app) photoPage(c *ui.Context, v *viewer) {
	stage := ui.Box(c).Fill().Background(ui.Hex("#0e0f13")).Clip().
		Focusable().AutoFocus().FocusRing(false)
	// The stage's box, from the frame it last showed in: what the zoom
	// lays the photo out in.
	bounds := stage.Bounds()
	stage.HandleInput(func(ev ui.InputEvent) bool {
		v := a.main.viewer
		if v == nil || v.bm == nil {
			return false
		}
		switch ev.Kind {
		case ui.InputScroll:
			if ev.DY == 0 || ev.Momentum {
				// Fingers off the touchpad: the glide is no zooming.
				return false
			}
			// A notch, or a touchpad's scroll, a little either way.
			factor := float32(math.Exp(-float64(ev.DY) * 0.002))
			a.zoomViewer(v.zoomFactor()*factor, ev.X, ev.Y, bounds)
			return true
		case ui.InputZoom:
			a.zoomViewer(v.zoomFactor()*ev.Scale, ev.X, ev.Y, bounds)
			return true
		}
		return false
	})
	// A drag over the photo moves the zoomed one under the pointer.
	dx, dy, dragging := stage.Dragged()
	if dragging && v.zoomFactor() > 1 {
		v.panX += dx
		v.panY += dy
	}
	stage.Children(func() {
		switch {
		case v.err != nil:
			a.viewerError(c, v)
		case v.bm != nil:
			lay := v.layout(bounds, v.bm)
			if lay.w > 0 {
				ui.Image(c, v.bm).Absolute().Left(lay.x).Top(lay.y).
					Width(lay.w).Height(lay.h).Fit(ui.Cover)
			} else {
				// The first frame, before the stage's box is known.
				ui.Box(c).Fill().Padding(viewerPad).Children(func() {
					ui.Image(c, v.bm).Fill().Fit(ui.Contain)
				})
			}
			if p := v.photo(); p != nil && p.Video {
				a.viewerVideoMark(c, p.Duration)
			}
			if lay.maxX > 0 || lay.maxY > 0 {
				if dragging {
					stage.Cursor(ui.CursorGrabbing)
				} else {
					stage.Cursor(ui.CursorGrab)
				}
			}
		default:
			ui.Column(c).Fill().Center().Gap(10).Children(func() {
				ui.Spinner(c).Label("正在加载图片").TextColor(ui.RGBA(255, 255, 255, 0.6))
				ui.Text(c, "正在加载图片…").FontSize(12).TextColor(ui.RGBA(255, 255, 255, 0.6))
			})
		}
		// The arrows keep to the stage's sides, clear of what it centers.
		ui.Column(c).Absolute().Left(0).Top(0).Bottom(0).Width(72).Center().Children(func() {
			a.viewerArrow(c, -1, backIcon)
		})
		ui.Column(c).Absolute().Right(0).Top(0).Bottom(0).Width(72).Center().Children(func() {
			a.viewerArrow(c, 1, forwardIcon)
		})
		if v.bm != nil {
			a.viewerZoomBar(c, v, bounds)
		}
	})
	// A double click on the photo zooms to twice the fit, and back to it.
	// The buttons keep theirs, which step the zoom instead.
	if stage.DoubleClicked() {
		if px, py, ok := stage.PointerPosition(); ok {
			if v.zoomFactor() > 1 {
				a.resetViewerZoom()
			} else {
				a.zoomViewer(2, px, py, bounds)
			}
		}
	}
	if stage.Shortcut(0, ui.KeyLeft) {
		a.viewerMove(-1)
	}
	if stage.Shortcut(0, ui.KeyRight) {
		a.viewerMove(1)
	}
	if stage.Shortcut(0, ui.KeyEqual) || stage.Shortcut(ui.Shift, ui.KeyEqual) {
		a.zoomViewer(v.zoomFactor()*zoomStep, bounds.W/2, bounds.H/2, bounds)
	}
	if stage.Shortcut(0, ui.KeyMinus) {
		a.zoomViewer(v.zoomFactor()/zoomStep, bounds.W/2, bounds.H/2, bounds)
	}
	if stage.Shortcut(0, ui.Key0) {
		a.resetViewerZoom()
	}
	if stage.Shortcut(0, ui.KeyEscape) {
		a.closeViewer()
	}
}

// viewerLayout is where the photo draws on the stage, and how far it may
// pan from the center.
type viewerLayout struct {
	x, y, w, h float32
	maxX, maxY float32
}

// layout sizes the photo at its zoom, centers it, and keeps its pan within
// the room the zoomed photo has around the stage's center.
func (v *viewer) layout(stage ui.Rect, bm *ui.Bitmap) viewerLayout {
	innerW := max(stage.W-2*viewerPad, 0)
	innerH := max(stage.H-2*viewerPad, 0)
	iw, ih := bm.Size()
	if innerW <= 0 || innerH <= 0 || iw <= 0 || ih <= 0 {
		return viewerLayout{}
	}
	fw, fh := float32(iw), float32(ih)
	fit := min(innerW/fw, innerH/fh)
	z := v.zoomFactor()
	w, h := fw*fit*z, fh*fit*z
	maxX := max((w-innerW)/2, 0)
	maxY := max((h-innerH)/2, 0)
	v.panX = min(max(v.panX, -maxX), maxX)
	v.panY = min(max(v.panY, -maxY), maxY)
	return viewerLayout{
		x:    (stage.W-w)/2 + v.panX,
		y:    (stage.H-h)/2 + v.panY,
		w:    w,
		h:    h,
		maxX: maxX,
		maxY: maxY,
	}
}

// zoomViewer zooms the photo to z around the stage point (px, py), keeping
// the point of the photo under the pointer where it is.
func (a *app) zoomViewer(z, px, py float32, stage ui.Rect) {
	v := a.main.viewer
	if v == nil || v.bm == nil {
		return
	}
	z = min(max(z, 1), maxViewerZoom)
	z0 := v.zoomFactor()
	if z == z0 {
		return
	}
	innerW := max(stage.W-2*viewerPad, 0)
	innerH := max(stage.H-2*viewerPad, 0)
	if innerW <= 0 || innerH <= 0 {
		return
	}
	cx := px - viewerPad - innerW/2
	cy := py - viewerPad - innerH/2
	v.panX += (cx - v.panX) * (1 - z/z0)
	v.panY += (cy - v.panY) * (1 - z/z0)
	v.zoom = z
	v.layout(stage, v.bm)
}

// resetViewerZoom shows the photo at fit again.
func (a *app) resetViewerZoom() {
	if v := a.main.viewer; v != nil {
		v.zoom, v.panX, v.panY = 1, 0, 0
	}
}

// viewerZoomBar is the zoom's controls at the stage's bottom right: out,
// the percentage that goes back to fit, and in.
func (a *app) viewerZoomBar(c *ui.Context, v *viewer, stage ui.Rect) {
	z := v.zoomFactor()
	ui.Row(c).Absolute().Right(16).Bottom(16).Height(30).Padding(0, 4).Gap(2).
		Radius(15).Background(ui.RGBA(0, 0, 0, 0.55)).AlignItems(ui.Center).Children(func() {
		a.viewerZoomButton(c, minusIcon, "缩小", z > 1, func() {
			a.zoomViewer(z/zoomStep, stage.W/2, stage.H/2, stage)
		})
		pct := ui.ButtonBase(c).Height(24).Padding(0, 8).Radius(6).Center().
			Label("适应窗口").Tooltip("适应窗口")
		fg := ui.RGBA(255, 255, 255, 0.92)
		if z <= 1 {
			fg = ui.RGBA(255, 255, 255, 0.55)
		} else if pct.Pressed() {
			pct.Background(ui.RGBA(255, 255, 255, 0.22))
		} else if pct.Hovered() {
			pct.Background(ui.RGBA(255, 255, 255, 0.14))
		}
		pct.Children(func() {
			ui.Text(c, fmt.Sprintf("%d%%", int(math.Round(float64(z)*100)))).
				FontSize(11).TextColor(fg)
		})
		if z > 1 && pct.Clicked() {
			a.resetViewerZoom()
		}
		a.viewerZoomButton(c, plusIcon, "放大", z < maxViewerZoom, func() {
			a.zoomViewer(z*zoomStep, stage.W/2, stage.H/2, stage)
		})
	})
}

// viewerZoomButton is one of the zoom bar's round buttons.
func (a *app) viewerZoomButton(c *ui.Context, icon *ui.SVG, label string, enabled bool, fn func()) {
	b := ui.ButtonBase(c).Size(24, 24).Radius(6).Center().Label(label).Tooltip(label)
	fg := ui.RGBA(255, 255, 255, 0.92)
	if enabled {
		if b.Pressed() {
			b.Background(ui.RGBA(255, 255, 255, 0.22))
		} else if b.Hovered() {
			b.Background(ui.RGBA(255, 255, 255, 0.14))
		}
	} else {
		b.Disabled(true)
		fg = ui.RGBA(255, 255, 255, 0.28)
	}
	b.Children(func() { ui.Icon(c, icon).Size(13, 13).TextColor(fg) })
	if enabled && b.Clicked() {
		fn()
	}
}

// viewerArrow is one of the round buttons that walk the page's photos; the
// end of the timeline dims it.
func (a *app) viewerArrow(c *ui.Context, delta int, icon *ui.SVG) {
	label := "下一张"
	if delta < 0 {
		label = "上一张"
	}
	_, enabled := a.viewerNeighbor(delta)
	b := ui.ButtonBase(c).Size(40, 40).Radius(20).Center().Label(label).Tooltip(label).
		Background(ui.RGBA(255, 255, 255, 0.10))
	fg := ui.RGBA(255, 255, 255, 0.92)
	if enabled {
		if b.Pressed() {
			b.Background(ui.RGBA(255, 255, 255, 0.22))
		} else if b.Hovered() {
			b.Background(ui.RGBA(255, 255, 255, 0.17))
		}
	} else {
		b.Disabled(true)
		fg = ui.RGBA(255, 255, 255, 0.28)
	}
	b.Children(func() { ui.Icon(c, icon).Size(18, 18).TextColor(fg) })
	if enabled && b.Clicked() {
		a.viewerMove(delta)
	}
}

// viewerError tells why the image did not come, and offers to try again.
func (a *app) viewerError(c *ui.Context, v *viewer) {
	title, action, fn := "无法显示这张图片", "重试", a.reloadViewerPhoto
	if errors.Is(v.err, errSessionExpired) {
		title, action, fn = "登录已过期", "重新登录", a.signOut
	}
	ui.Column(c).Fill().Center().Gap(8).Padding(24).Children(func() {
		ui.Box(c).Size(48, 48).Radius(12).Background(ui.RGBA(255, 255, 255, 0.08)).Center().
			Children(func() {
				ui.Icon(c, errorIcon).Size(22, 22).TextColor(ui.RGBA(255, 255, 255, 0.55))
			})
		ui.Text(c, title).FontSize(17).FontWeight(600).TextColor(ui.Hex("#ffffff"))
		ui.Text(c, v.err.Error()).FontSize(12.5).TextColor(ui.RGBA(255, 255, 255, 0.55)).
			MaxWidth(420).TextAlign(ui.Center)
		ui.Box(c).Height(4)
		ui.PrimaryButton(c, action).OnClick(fn)
	})
}

// viewerVideoMark notes that the still is a video's, and how long it runs.
func (a *app) viewerVideoMark(c *ui.Context, ms int) {
	text := "视频"
	if ms > 0 {
		text += " · " + durationLabel(ms)
	}
	ui.Row(c).Absolute().Left(20).Bottom(20).Height(26).Padding(0, 10).Gap(5).Radius(13).
		Background(ui.RGBA(0, 0, 0, 0.55)).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, playIcon).Size(10, 10).TextColor(ui.Hex("#ffffff"))
		ui.Text(c, text).FontSize(11.5).TextColor(ui.Hex("#ffffff"))
	})
}

// viewerCaption is the day of the photo shown, as the page's header says it.
func (a *app) viewerCaption() (title, week string) {
	if v := a.main.viewer; v != nil {
		return bucketTitle(v.dayKey, time.Now())
	}
	return "", ""
}

// viewerCounter is how far along the loaded timeline the photo shown is.
func (a *app) viewerCounter() string {
	if n, total := a.viewerPosition(); n > 0 {
		return fmt.Sprintf("%d / %d", n, total)
	}
	return ""
}
