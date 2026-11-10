package main

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// The image page keeps this much room around the photo at fit, in DIPs, and
// grows it to maxViewerZoom times the fit, a zoomStep a click.
const (
	viewerPad     = 20
	maxViewerZoom = 6
	zoomStep      = 1.25
	// detailsW is the width of the details panel, in DIPs.
	detailsW = 300
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
	// showDetails tells whether the details panel is out; details is what
	// it shows once loaded, and detailsErr why it did not.
	showDetails    bool
	details        *assetDetails
	detailsErr     error
	detailsAttempt int
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
	changed := v.id != id
	v.feed = f
	v.dayKey = dayKey
	v.id = id
	v.bm = nil
	v.err = nil
	v.zoom, v.panX, v.panY = 1, 0, 0
	if changed {
		// Another asset: its details are not this one's.
		v.details = nil
		v.detailsErr = nil
		v.detailsAttempt++
		if v.showDetails {
			a.loadDetails(v)
		}
	}
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

// toggleDetails shows or hides the details panel, loading what it tells the
// first time.
func (a *app) toggleDetails() {
	v := a.main.viewer
	if v == nil {
		return
	}
	v.showDetails = !v.showDetails
	if v.showDetails && v.details == nil && v.detailsErr == nil {
		a.loadDetails(v)
	}
}

// loadDetails loads what the details panel tells of the asset shown, off
// the UI thread and published through app.update.
func (a *app) loadDetails(v *viewer) {
	if a.login.user == nil || a.login.user.Token == "" {
		return
	}
	v.detailsErr = nil
	v.detailsAttempt++
	attempt := v.detailsAttempt
	id := v.id
	base, token := a.login.serverURL, a.login.user.Token
	go func() {
		d, err := fetchAssetDetails(base, token, id)
		a.update(func() {
			if a.main.viewer != v || attempt != v.detailsAttempt || v.id != id {
				return
			}
			if err != nil {
				v.detailsErr = err
				return
			}
			v.details = d
		})
	}()
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
	// The details panel stands over the stage's right side; what it covers
	// does not zoom.
	panel := float32(0)
	if v.showDetails {
		panel = detailsW
	}
	// The photo lays out left of the panel while it is out.
	photoStage := bounds
	photoStage.W = max(bounds.W-panel, 0)
	overPanel := func() bool {
		if panel == 0 {
			return false
		}
		px, _, ok := stage.PointerPosition()
		return ok && px >= bounds.W-panel
	}
	stage.HandleInput(func(ev ui.InputEvent) bool {
		v := a.main.viewer
		if v == nil || v.bm == nil || ev.X >= bounds.W-panel {
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
			a.zoomViewer(v.zoomFactor()*factor, ev.X, ev.Y, photoStage)
			return true
		case ui.InputZoom:
			a.zoomViewer(v.zoomFactor()*ev.Scale, ev.X, ev.Y, photoStage)
			return true
		}
		return false
	})
	// A drag over the photo moves the zoomed one under the pointer.
	dx, dy, dragging := stage.Dragged()
	if dragging && v.zoomFactor() > 1 && !overPanel() {
		v.panX += dx
		v.panY += dy
	}
	stage.Children(func() {
		switch {
		case v.err != nil:
			a.viewerError(c, v)
		case v.bm != nil:
			lay := v.layout(photoStage, v.bm)
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
		ui.Column(c).Absolute().Right(panel).Top(0).Bottom(0).Width(72).Center().Children(func() {
			a.viewerArrow(c, 1, forwardIcon)
		})
		if v.bm != nil {
			a.viewerZoomBar(c, v, photoStage, 16+panel)
		}
		if v.showDetails {
			a.viewerDetails(c, v)
		}
	})
	// A double click on the photo zooms to twice the fit, and back to it.
	// The buttons keep theirs, which step the zoom instead.
	if stage.DoubleClicked() {
		if px, py, ok := stage.PointerPosition(); ok {
			if v.zoomFactor() > 1 {
				a.resetViewerZoom()
			} else {
				a.zoomViewer(2, px, py, photoStage)
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
		a.zoomViewer(v.zoomFactor()*zoomStep, photoStage.W/2, photoStage.H/2, photoStage)
	}
	if stage.Shortcut(0, ui.KeyMinus) {
		a.zoomViewer(v.zoomFactor()/zoomStep, photoStage.W/2, photoStage.H/2, photoStage)
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
func (a *app) viewerZoomBar(c *ui.Context, v *viewer, stage ui.Rect, right float32) {
	z := v.zoomFactor()
	ui.Row(c).Absolute().Right(right).Bottom(16).Height(30).Padding(0, 4).Gap(2).
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

// detailsButton is the page's header button for the details panel, lit
// while the panel is out.
func (a *app) detailsButton(c *ui.Context, open bool) {
	t := c.Theme()
	label := "详情"
	if open {
		label = "隐藏详情"
	}
	b := ui.ButtonBase(c).Size(28, 26).Radius(6).Center().Label(label).Tooltip(label)
	switch {
	case b.Pressed():
		b.Background(t.SurfacePressed)
	case b.Hovered() || open:
		b.Background(t.SurfaceHover)
	}
	fg := t.TextMuted
	if open {
		fg = t.Text
	}
	b.Children(func() { ui.Icon(c, infoIcon).Size(15, 15).TextColor(fg) })
	if b.Clicked() {
		a.toggleDetails()
	}
}

// viewerDetails is the panel at the stage's right: what the asset says of
// itself, from its name and time to its camera and place.
func (a *app) viewerDetails(c *ui.Context, v *viewer) {
	ui.Column(c).Absolute().Right(0).Top(0).Bottom(0).Width(detailsW).
		Background(ui.RGBA(13, 14, 18, 0.96)).
		Border(1, ui.RGBA(255, 255, 255, 0.08)).BorderWidth(0, 0, 0, 1).
		Padding(16, 16, 12, 16).Children(func() {
		ui.Row(c).Height(26).Shrink(0).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "详情").FontSize(13.5).FontWeight(600).TextColor(ui.Hex("#ffffff"))
			ui.Spacer(c)
			b := ui.ButtonBase(c).Size(24, 24).Radius(6).Center().
				Label("关闭详情").Tooltip("关闭详情")
			if b.Pressed() {
				b.Background(ui.RGBA(255, 255, 255, 0.2))
			} else if b.Hovered() {
				b.Background(ui.RGBA(255, 255, 255, 0.12))
			}
			b.Children(func() {
				ui.Icon(c, closeIcon).Size(13, 13).TextColor(ui.RGBA(255, 255, 255, 0.65))
			})
			if b.Clicked() {
				a.toggleDetails()
			}
		})
		switch {
		case v.detailsErr != nil:
			a.viewerDetailsError(c, v)
		case v.details == nil:
			ui.Column(c).Grow(1).Center().Gap(10).Children(func() {
				ui.Spinner(c).Label("正在加载详情").TextColor(ui.RGBA(255, 255, 255, 0.6))
				ui.Text(c, "正在加载详情…").FontSize(11.5).TextColor(ui.RGBA(255, 255, 255, 0.6))
			})
		default:
			ui.Scroll(c).Grow(1).Children(func() {
				ui.Column(c).Gap(12).Padding(0, 4, 8, 0).Children(func() {
					for _, row := range v.detailRows() {
						ui.Column(c).Gap(2).Children(func() {
							ui.Text(c, row.label).FontSize(10.5).
								TextColor(ui.RGBA(255, 255, 255, 0.45))
							ui.Text(c, row.value).FontSize(12.5).TextColor(ui.Hex("#ffffff"))
						})
					}
				})
			})
		}
	})
}

// viewerDetailsError tells why the details did not come, and offers to try
// again.
func (a *app) viewerDetailsError(c *ui.Context, v *viewer) {
	title, action, fn := "无法加载详情", "重试", func() { a.loadDetails(v) }
	if errors.Is(v.detailsErr, errSessionExpired) {
		title, action, fn = "登录已过期", "重新登录", a.signOut
	}
	ui.Column(c).Grow(1).Center().Gap(8).Padding(8).Children(func() {
		ui.Text(c, title).FontSize(13).FontWeight(600).TextColor(ui.Hex("#ffffff"))
		ui.Text(c, v.detailsErr.Error()).FontSize(11.5).
			TextColor(ui.RGBA(255, 255, 255, 0.5)).
			MaxWidth(detailsW - 48).TextAlign(ui.Center)
		ui.PrimaryButton(c, action).OnClick(fn)
	})
}

// detailRow is one line the details panel tells: what it is, and its value.
type detailRow struct {
	label string
	value string
}

// detailRows is what the details panel tells of the asset shown, left out
// where the server had nothing.
func (v *viewer) detailRows() []detailRow {
	d := v.details
	if d == nil {
		return nil
	}
	rows := make([]detailRow, 0, 12)
	add := func(label, value string) {
		if value != "" {
			rows = append(rows, detailRow{label: label, value: value})
		}
	}
	add("文件名", d.Name)
	if !d.Taken.IsZero() {
		taken := d.Taken.Format("2006年1月2日 15:04")
		if d.TimeZone != "" {
			taken += " · " + d.TimeZone
		}
		add("拍摄时间", taken)
	}
	if d.Width > 0 && d.Height > 0 {
		size := fmt.Sprintf("%d × %d", d.Width, d.Height)
		if mp := float64(d.Width) * float64(d.Height) / 1e6; mp >= 0.1 {
			size += fmt.Sprintf(" · %.1f MP", mp)
		}
		add("尺寸", size)
	}
	if d.Size > 0 {
		add("大小", byteSize(d.Size))
	}
	add("相机", cameraName(d.Make, d.Model))
	add("镜头", d.Lens)
	add("曝光", exposureLine(d))
	add("位置", locationLine(d))
	add("描述", d.Description)
	if d.Duration > 0 {
		add("时长", durationLabel(d.Duration))
	}
	return rows
}

// cameraName is a camera's make and model, the make once.
func cameraName(make, model string) string {
	make, model = strings.TrimSpace(make), strings.TrimSpace(model)
	switch {
	case make == "":
		return model
	case model == "":
		return make
	case strings.HasPrefix(strings.ToLower(model), strings.ToLower(make)):
		return model
	}
	return make + " " + model
}

// exposureLine is a photo's camera settings, as photographers read them.
func exposureLine(d *assetDetails) string {
	var parts []string
	if d.FNumber > 0 {
		parts = append(parts, "f/"+strconv.FormatFloat(d.FNumber, 'f', -1, 64))
	}
	if e := strings.TrimSpace(d.Exposure); e != "" {
		if _, err := strconv.ParseFloat(e, 64); err == nil {
			e += " 秒"
		}
		parts = append(parts, e)
	}
	if d.ISO > 0 {
		parts = append(parts, fmt.Sprintf("ISO %d", d.ISO))
	}
	if d.Focal > 0 {
		parts = append(parts, strconv.FormatFloat(d.Focal, 'f', -1, 64)+" mm")
	}
	return strings.Join(parts, " · ")
}

// locationLine is where a photo was taken: its city, state and country, or
// its coordinates when it names no place.
func locationLine(d *assetDetails) string {
	var parts []string
	for _, s := range []string{d.City, d.State, d.Country} {
		if s = strings.TrimSpace(s); s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, " · ")
	}
	if d.Latitude != nil && d.Longitude != nil {
		return fmt.Sprintf("%.5f, %.5f", *d.Latitude, *d.Longitude)
	}
	return ""
}

// byteSize is a file's size, as the details panel tells it.
func byteSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
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
