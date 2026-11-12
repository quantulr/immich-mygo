package main

import (
	"errors"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
)

// The image page keeps this much room around the photo at fit, in DIPs, and
// grows it to maxViewerZoom times the fit, a zoomStep a click.
const (
	viewerPad     = 20
	maxViewerZoom = 6
	zoomStep      = 1.25
	// detailsW is the width of the details panel, in DIPs, detailsMargin how
	// far it stands from the stage's top, bottom and right edges, and
	// detailsSlide how long it takes to slide in or out, the photo making
	// room as it goes.
	detailsW      = 300
	detailsMargin = 12
	detailsSlide  = 200 * time.Millisecond
	// livePressDelay is how long a press on a live photo's still lasts
	// before its motion plays, and livePressSlop how far it may wander
	// before the press is a drag.
	livePressDelay = 300 * time.Millisecond
	livePressSlop  = 8
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
	// focusStage asks the next frame to give the stage the keyboard, as
	// the page opens: its box may take the place, and so the state, of
	// the list it replaced, which AutoFocus would take for an element
	// already there.
	focusStage bool
	// showDetails tells whether the details panel is out; details is what
	// it shows once loaded, and detailsErr why it did not.
	showDetails    bool
	details        *assetDetails
	detailsErr     error
	detailsAttempt int
	// favorite tells that the asset shown is one of the user's favorites,
	// and favoriteBusy that a change of the mark is on its way.
	favorite     bool
	favoriteBusy bool
	// liveAt is when the press that shows a live photo's motion began,
	// zero while none does, where it began, and liveOn tells that the
	// motion plays.
	liveAt time.Time
	liveX  float32
	liveY  float32
	liveOn bool
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
	a.main.viewer = &viewer{focusStage: true, showDetails: a.preferences.DetailsOpen}
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
	// A video plays in the system's player, which reads it through the
	// loopback server; another asset closes it.
	if p := v.photo(); p != nil && p.Video {
		a.playVideo(id)
	} else {
		a.stopVideo()
	}
	if changed {
		// Another asset: its details are not this one's, nor its favorite
		// mark.
		v.details = nil
		v.detailsErr = nil
		v.detailsAttempt++
		v.favorite = false
		v.favoriteBusy = false
		if p := v.photo(); p != nil {
			v.favorite = p.Favorite
		}
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
	a.stopVideo()
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
	a.preferences.DetailsOpen = v.showDetails
	if err := appPrefs.save(a.preferences); err != nil {
		log.Printf("saving the preferences: %v", err)
	}
	if v.showDetails && v.details == nil && v.detailsErr == nil {
		a.loadDetails(v)
	}
}

// toggleFavorite marks the asset shown as a favorite, or takes the mark
// away, off the UI thread, and keeps what the server says of it.
func (a *app) toggleFavorite(v *viewer) {
	if v == nil || v.favoriteBusy || a.login.user == nil || a.login.user.Token == "" {
		return
	}
	id, favorite := v.id, !v.favorite
	v.favoriteBusy = true
	base, token := a.login.serverURL, a.login.user.Token
	go func() {
		got, err := setAssetFavorite(base, token, id, favorite)
		a.update(func() {
			if a.main.viewer != v || v.id != id {
				return
			}
			v.favoriteBusy = false
			if err != nil {
				a.notice = "无法更新收藏：" + err.Error()
				return
			}
			v.favorite = got
			// The timeline the page walks keeps the mark too, so that the
			// asset shows it when the page comes back to it.
			if p := v.photo(); p != nil {
				p.Favorite = got
			}
		})
	}()
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
	p := v.photo()
	isVideo := p != nil && p.Video
	// bm is what the stage shows: the larger image once it loads, or the
	// thumbnail standing in for it, so that the photo flies in from its
	// tile at once, not when the larger image comes.
	bm := v.bm
	if bm == nil && p != nil && !isVideo {
		bm = p.Bm
	}
	stage := ui.Box(c).Fill().Background(ui.Hex("#0e0f13")).Clip().
		Focusable().FocusRing(false)
	if v.focusStage {
		stage.Focus()
		v.focusStage = false
	}
	// The stage's box, from the frame it last showed in: what the zoom
	// lays the photo out in.
	bounds := stage.Bounds()
	// The details panel stands over the stage's right side, clear of its
	// edges; what it covers does not zoom. It slides in and out, and the
	// photo makes room as it goes.
	panelTarget := float32(0)
	if v.showDetails {
		panelTarget = detailsW + detailsMargin
	}
	panel := stage.Animate("details-panel", panelTarget, detailsSlide)
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
	// livePlaying tells that a live photo's motion shows, as its still is
	// held.
	livePlaying := !isVideo && v.liveOn && v.bm != nil && a.video.player != nil
	stage.HandleInput(func(ev ui.InputEvent) bool {
		v := a.main.viewer
		// A video is the player's, which sizes it to the stage: the wheel and
		// a pinch leave it at fit, as they leave a live photo's motion.
		if v == nil || v.bm == nil || isVideo || livePlaying || ev.X >= bounds.W-panel {
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
	if !isVideo && dragging && v.zoomFactor() > 1 && !overPanel() {
		v.panX += dx
		v.panY += dy
	}
	stage.Children(func() {
		switch {
		case isVideo && a.video.err != nil:
			ui.Box(c).Fill().Padding(0, panel, 0, 0).Children(func() {
				a.viewerVideoError(c, a.video.err)
			})
		case isVideo && a.video.player != nil:
			a.viewerVideo(c, v, photoStage, panel)
		case v.err != nil:
			// The load states center in the photo's room, not the panel's.
			ui.Box(c).Fill().Padding(0, panel, 0, 0).Children(func() {
				a.viewerError(c, v)
			})
		case bm != nil:
			lay := v.layout(photoStage, bm)
			switch {
			case livePlaying:
				a.viewerLiveVideo(c, v, lay)
			case lay.w > 0:
				img := ui.Image(c, bm).Absolute().Left(lay.x).Top(lay.y).
					Width(lay.w).Height(lay.h).Fit(ui.Cover)
				if !isVideo && v.zoomFactor() == 1 && !livePlaying {
					img.Transition(photoFlight(v.id))
				}
			default:
				// The first frame, before the stage's box is known.
				ui.Box(c).Fill().Padding(viewerPad).Children(func() {
					img := ui.Image(c, bm).Fill().Fit(ui.Contain)
					if !isVideo && !livePlaying {
						img.Transition(photoFlight(v.id))
					}
				})
			}
			if isVideo {
				a.viewerVideoMark(c, p.Duration)
			} else {
				if p.Live && !livePlaying {
					a.viewerLiveMark(c)
				}
				if !livePlaying && (lay.maxX > 0 || lay.maxY > 0) {
					if dragging {
						stage.Cursor(ui.CursorGrabbing)
					} else {
						stage.Cursor(ui.CursorGrab)
					}
				}
			}
		default:
			ui.Column(c).Fill().Padding(0, panel, 0, 0).Center().Gap(10).Children(func() {
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
		if v.bm != nil && !isVideo && !livePlaying {
			a.viewerZoomBar(c, v, photoStage, 16+panel)
		}
		if v.showDetails {
			a.viewerDetails(c, v)
		}
	})
	// A press held on a live photo's still plays its motion, as iOS does.
	if !isVideo && p != nil && p.LiveVideo != "" && v.bm != nil {
		a.holdLivePhoto(c, v, p, stage, photoStage)
	}
	// A double click on the photo zooms to twice the fit, and back to it.
	// The buttons keep theirs, which step the zoom instead.
	if !isVideo && stage.DoubleClicked() {
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
	if !isVideo {
		if stage.Shortcut(0, ui.KeyEqual) || stage.Shortcut(ui.Shift, ui.KeyEqual) {
			a.zoomViewer(v.zoomFactor()*zoomStep, photoStage.W/2, photoStage.H/2, photoStage)
		}
		if stage.Shortcut(0, ui.KeyMinus) {
			a.zoomViewer(v.zoomFactor()/zoomStep, photoStage.W/2, photoStage.H/2, photoStage)
		}
		if stage.Shortcut(0, ui.Key0) {
			a.resetViewerZoom()
		}
	}
	if isVideo && stage.Shortcut(0, ui.KeySpace) {
		a.toggleVideoPlayback()
	}
	if stage.Shortcut(0, ui.KeyEscape) {
		a.closeViewer()
	}
}

// toggleVideoPlayback plays or pauses the video shown, as its button does.
func (a *app) toggleVideoPlayback() {
	p := a.video.player
	if p == nil {
		return
	}
	if p.Playing() {
		p.Pause()
	} else {
		p.Play()
	}
}

// viewerVideo shows the video of the asset on the stage: the system's
// player in the photo's room, the still until its first picture comes, and
// the controls at the bottom once it plays.
func (a *app) viewerVideo(c *ui.Context, v *viewer, stage ui.Rect, panel float32) {
	p := a.video.player
	ui.Box(c).Fill().Padding(0, panel, 0, 0).Children(func() {
		ui.Box(c).Fill().Padding(viewerPad).Children(func() {
			ui.Video(c, p).Fill().Fit(ui.Contain).Label("视频")
			switch {
			case p.Err() != nil:
				a.viewerVideoError(c, p.Err())
			case !p.Ready():
				// The still stands in for the picture until the first comes.
				if v.bm != nil {
					ui.Image(c, v.bm).Absolute().Left(0).Top(0).Right(0).Bottom(0).
						Fit(ui.Contain)
				}
				a.viewerVideoWaiting(c)
			case p.Buffering():
				a.viewerVideoWaiting(c)
			}
		})
	})
	if p.Err() == nil && p.Ready() {
		a.viewerVideoControls(c, p, stage)
	}
}

// viewerVideoWaiting says that the video is on its way, over its still.
func (a *app) viewerVideoWaiting(c *ui.Context) {
	ui.Column(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).Center().Gap(10).Children(func() {
		ui.Spinner(c).Label("正在加载视频").TextColor(ui.RGBA(255, 255, 255, 0.6))
		ui.Text(c, "正在加载视频…").FontSize(12).TextColor(ui.RGBA(255, 255, 255, 0.6))
	})
}

// viewerVideoError tells why the video did not play, and offers to try
// again.
func (a *app) viewerVideoError(c *ui.Context, err error) {
	ui.Column(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).Center().Gap(8).Padding(24).
		Children(func() {
			ui.Box(c).Size(48, 48).Radius(12).Background(ui.RGBA(255, 255, 255, 0.08)).Center().
				Children(func() {
					ui.Icon(c, playIcon).Size(22, 22).TextColor(ui.RGBA(255, 255, 255, 0.55))
				})
			ui.Text(c, "无法播放视频").FontSize(17).FontWeight(600).TextColor(ui.Hex("#ffffff"))
			ui.Text(c, err.Error()).FontSize(12.5).TextColor(ui.RGBA(255, 255, 255, 0.55)).
				MaxWidth(420).TextAlign(ui.Center)
			ui.Box(c).Height(4)
			ui.PrimaryButton(c, "重试").OnClick(a.retryVideo)
		})
}

// viewerVideoControls is the video's bar at the stage's bottom: play or
// pause, where it plays and how long it runs, a seek slider, and mute.
func (a *app) viewerVideoControls(c *ui.Context, p *ui.VideoPlayer, stage ui.Rect) {
	pos, dur := p.Position(), p.Duration()
	w := min(max(stage.W-2*72, 260), 560)
	w = min(w, stage.W)
	ui.Row(c).Absolute().Left((stage.W-w)/2).Bottom(16).Width(w).Height(36).
		Padding(0, 8).Gap(10).Radius(18).Background(ui.RGBA(0, 0, 0, 0.55)).
		AlignItems(ui.Center).Children(func() {
		icon, label := pauseIcon, "暂停"
		if !p.Playing() {
			icon, label = playIcon, "播放"
		}
		a.viewerVideoButton(c, icon, label, a.toggleVideoPlayback)
		if w >= 300 {
			ui.Text(c, durationLabel(int(pos/time.Millisecond))+" / "+
				durationLabel(int(dur/time.Millisecond))).FontSize(11.5).
				TextColor(ui.RGBA(255, 255, 255, 0.8)).FontFeatures("tnum")
		}
		sec := pos.Seconds()
		if ui.Slider(c.Key("video-seek"), &sec, 0, max(dur.Seconds(), 0.001)).
			Grow(1).MinWidth(0).Label("播放位置").Changed() {
			p.Seek(time.Duration(sec * float64(time.Second)))
		}
		icon, label = soundIcon, "静音"
		if p.Muted() {
			icon, label = mutedIcon, "取消静音"
		}
		a.viewerVideoButton(c, icon, label, a.toggleVideoMuted)
	})
}

// viewerVideoButton is one of the video's round buttons.
func (a *app) viewerVideoButton(c *ui.Context, icon *ui.SVG, label string, fn func()) {
	b := ui.ButtonBase(c).Size(24, 24).Radius(6).Center().Label(label).Tooltip(label).
		KeepFocus()
	if b.Pressed() {
		b.Background(ui.RGBA(255, 255, 255, 0.22))
	} else if b.Hovered() {
		b.Background(ui.RGBA(255, 255, 255, 0.14))
	}
	b.Children(func() { ui.Icon(c, icon).Size(14, 14).TextColor(ui.RGBA(255, 255, 255, 0.92)) })
	if b.Clicked() {
		fn()
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
			Label("适应窗口").Tooltip("适应窗口").KeepFocus()
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
	b := ui.ButtonBase(c).Size(24, 24).Radius(6).Center().Label(label).Tooltip(label).
		KeepFocus()
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
		Background(ui.RGBA(255, 255, 255, 0.10)).KeepFocus()
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

// favoriteButton is the page's header button for the favorite mark: a red
// heart while the asset is a favorite, a quiet outline while it is not.
func (a *app) favoriteButton(c *ui.Context, v *viewer) {
	t := c.Theme()
	label := "收藏"
	if v.favorite {
		label = "取消收藏"
	}
	b := ui.ButtonBase(c).Size(28, 26).Radius(6).Center().Label(label).Tooltip(label).
		KeepFocus()
	switch {
	case b.Pressed():
		b.Background(t.SurfacePressed)
	case b.Hovered() || v.favorite:
		b.Background(t.SurfaceHover)
	}
	fg, icon := t.TextMuted, favoritesIcon
	if v.favorite {
		fg, icon = t.Danger, favoritesOnIcon
	}
	b.Children(func() {
		heart := ui.Icon(c, icon).Size(15, 15).TextColor(fg)
		if v.favoriteBusy {
			heart.Opacity(0.5)
		}
	})
	if !v.favoriteBusy && b.Clicked() {
		a.toggleFavorite(v)
	}
}

// detailsButton is the page's header button for the details panel, lit
// while the panel is out.
func (a *app) detailsButton(c *ui.Context, open bool) {
	t := c.Theme()
	label := "详情"
	if open {
		label = "隐藏详情"
	}
	b := ui.ButtonBase(c).Size(28, 26).Radius(6).Center().Label(label).Tooltip(label).
		KeepFocus()
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

// detailsTheme is the theme the details panel builds with: dark whatever
// the window's, as it stands on the dark image page, and white text over a
// light pane would wash out.
var detailsTheme = ui.DarkTheme()

// viewerDetails is the panel at the stage's right: what the asset says of
// itself, from its name and time to its camera and place. It is a pane of
// dark liquid glass, its corners round and its edges clear of the stage's,
// over the photo; it slides in from the right edge as it shows and back out
// as it goes.
func (a *app) viewerDetails(c *ui.Context, v *viewer) {
	ui.Themed(c, detailsTheme).Key("viewer-details").Absolute().
		Right(detailsMargin).Top(detailsMargin).Bottom(detailsMargin).Width(detailsW).
		Radius(16).
		Material(glass.Glass{Tint: ui.RGBA(13, 14, 18, 0.5)}).
		Padding(16, 16, 12, 16).
		Transition(ui.ElementTransition{
			Duration: detailsSlide,
			Enter:    &ui.Motion{XPercent: 100, X: detailsMargin, Opacity: 1},
			Exit:     &ui.Motion{XPercent: 100, X: detailsMargin, Opacity: 1},
		}).Children(func() {
		ui.Row(c).Height(26).Shrink(0).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "详情").FontSize(13.5).FontWeight(600).TextColor(ui.Hex("#ffffff"))
			ui.Spacer(c)
			b := ui.ButtonBase(c).Size(24, 24).Radius(6).Center().
				Label("关闭详情").Tooltip("关闭详情").KeepFocus()
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
								TextColor(ui.RGBA(255, 255, 255, 0.55))
							if row.copyable {
								ui.Row(c).Gap(4).AlignItems(ui.Start).Children(func() {
									ui.Text(c, row.value).Grow(1).MinWidth(0).
										FontSize(12.5).TextColor(ui.Hex("#ffffff"))
									a.detailCopyButton(c, row.value)
								})
								return
							}
							ui.Text(c, row.value).FontSize(12.5).TextColor(ui.Hex("#ffffff"))
						})
					}
					if d := v.details; d != nil && len(d.Albums) > 0 {
						ui.Column(c).Gap(4).Children(func() {
							ui.Text(c, "相册").FontSize(10.5).
								TextColor(ui.RGBA(255, 255, 255, 0.55))
							for _, al := range d.Albums {
								a.detailAlbumRow(c, al)
							}
						})
					}
				})
			})
		}
	})
}

// detailCopyButton copies a value of the details panel, as the file's name,
// to the clipboard.
func (a *app) detailCopyButton(c *ui.Context, value string) {
	b := ui.ButtonBase(c).Size(18, 18).Shrink(0).Radius(4).Center().
		Label("复制文件名").Tooltip("复制文件名")
	if b.Pressed() {
		b.Background(ui.RGBA(255, 255, 255, 0.2))
	} else if b.Hovered() {
		b.Background(ui.RGBA(255, 255, 255, 0.12))
	}
	fg := ui.RGBA(255, 255, 255, 0.55)
	if b.Hovered() {
		fg = ui.RGBA(255, 255, 255, 0.85)
	}
	b.Children(func() { ui.Icon(c, copyIcon).Size(12, 12).TextColor(fg) })
	if b.Clicked() {
		c.WriteClipboard(value)
		c.Toast("已复制文件名")
	}
}

// detailAlbumRow shows an album that holds the asset: its cover, its name
// and how many assets it holds. A click opens the album's days.
func (a *app) detailAlbumRow(c *ui.Context, al assetAlbum) {
	row := ui.Row(c).Height(40).Padding(4).Gap(8).Radius(8).AlignItems(ui.Center).
		Background(ui.RGBA(255, 255, 255, 0.04)).Cursor(ui.CursorPointer).
		Label("打开相册 " + al.Name)
	if row.Hovered() {
		row.Background(ui.RGBA(255, 255, 255, 0.1))
	}
	row.Children(func() {
		cover := ui.Box(c).Size(30, 30).Shrink(0).Radius(6).Clip().
			Background(ui.RGBA(255, 255, 255, 0.1))
		if al.Bm != nil {
			cover.Children(func() { ui.Image(c, al.Bm).Fill().Fit(ui.Cover) })
		} else {
			cover.Center().Children(func() {
				ui.Icon(c, albumsIcon).Size(14, 14).TextColor(ui.RGBA(255, 255, 255, 0.45))
			})
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, al.Name).FontSize(12).SingleLine().Ellipsis("…").
				TextColor(ui.Hex("#ffffff"))
			if al.Count > 0 {
				ui.Text(c, fmt.Sprintf("%d 项", al.Count)).FontSize(10.5).
					TextColor(ui.RGBA(255, 255, 255, 0.55))
			}
		})
		ui.Icon(c, forwardIcon).Size(12, 12).TextColor(ui.RGBA(255, 255, 255, 0.3))
	})
	if row.Clicked() {
		a.openDetailAlbum(al)
	}
}

// openDetailAlbum shows the days of an album the image page lists, leaving
// the photo page for it.
func (a *app) openDetailAlbum(al assetAlbum) {
	a.selectSection(sectionAlbums)
	a.openAlbum(&album{ID: al.ID, Name: al.Name, Count: al.Count, Cover: al.Cover, Bm: al.Bm})
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
			TextColor(ui.RGBA(255, 255, 255, 0.55)).
			MaxWidth(detailsW - 48).TextAlign(ui.Center)
		ui.PrimaryButton(c, action).OnClick(fn)
	})
}

// detailRow is one line the details panel tells: what it is, and its value.
type detailRow struct {
	label string
	value string
	// copyable tells that the panel offers the value for copying, as it
	// does a file's name.
	copyable bool
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
	if d.Name != "" {
		rows = append(rows, detailRow{label: "文件名", value: d.Name, copyable: true})
	}
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

// holdLivePhoto plays a live photo's motion while its still is held for
// livePressDelay, and stops it as the press ends. A press that wanders is a
// drag, which pans the zoomed still instead.
func (a *app) holdLivePhoto(c *ui.Context, v *viewer, p *photo, stage ui.Element, photoStage ui.Rect) {
	if v.liveOn {
		if !stage.Pressed() {
			a.stopVideo()
			c.After(0)
		}
		return
	}
	if !stage.Pressed() {
		v.liveAt = time.Time{}
		return
	}
	px, py, ok := stage.PointerPosition()
	if !ok {
		v.liveAt = time.Time{}
		return
	}
	lay := v.layout(photoStage, v.bm)
	if lay.w <= 0 || px < lay.x || px >= lay.x+lay.w ||
		py < lay.y || py >= lay.y+lay.h {
		// The still is not under the pointer.
		v.liveAt = time.Time{}
		return
	}
	if v.liveAt.IsZero() {
		v.liveAt = c.Now()
		v.liveX, v.liveY = px, py
		c.After(livePressDelay)
		return
	}
	if dx, dy := px-v.liveX, py-v.liveY; dx > livePressSlop || dx < -livePressSlop ||
		dy > livePressSlop || dy < -livePressSlop {
		v.liveAt = time.Time{}
		return
	}
	if c.Now().Sub(v.liveAt) >= livePressDelay {
		v.liveAt = time.Time{}
		a.playLivePhoto(p.LiveVideo)
		c.After(0)
	}
}

// playLivePhoto plays the video of a live photo, as holding its still does:
// the page's player, without the video's controls.
func (a *app) playLivePhoto(id string) {
	a.stopVideo()
	a.playVideo(id)
	if v := a.main.viewer; v != nil {
		v.liveOn = true
	}
}

// viewerLiveVideo shows a live photo's motion in its still's room, the still
// standing in until the motion's first picture comes.
func (a *app) viewerLiveVideo(c *ui.Context, v *viewer, lay viewerLayout) {
	p := a.video.player
	if lay.w > 0 {
		ui.Video(c, p).Absolute().Left(lay.x).Top(lay.y).
			Width(lay.w).Height(lay.h).Fit(ui.Contain)
		if !p.Ready() {
			ui.Image(c, v.bm).Absolute().Left(lay.x).Top(lay.y).
				Width(lay.w).Height(lay.h).Fit(ui.Contain)
		}
		return
	}
	// The first frame, before the stage's box is known.
	ui.Box(c).Fill().Padding(viewerPad).Children(func() {
		ui.Video(c, p).Fill().Fit(ui.Contain)
		if !p.Ready() {
			ui.Image(c, v.bm).Fill().Fit(ui.Contain)
		}
	})
}

// viewerLiveMark notes that the still is a live photo's.
func (a *app) viewerLiveMark(c *ui.Context) {
	ui.Row(c).Absolute().Left(20).Bottom(20).Height(26).Padding(0, 10).Gap(5).Radius(13).
		Background(ui.RGBA(0, 0, 0, 0.55)).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, livePhotoIcon).Size(13, 13).TextColor(ui.Hex("#ffffff"))
		ui.Text(c, "实况照片").FontSize(11.5).TextColor(ui.Hex("#ffffff"))
	})
}

// viewerCaption is the day of the photo shown, as the page's header says it;
// a photo of the search results names the search instead.
func (a *app) viewerCaption() (title, week string) {
	if v := a.main.viewer; v != nil {
		if v.feed == &a.main.results.feed {
			return "搜索", ""
		}
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
