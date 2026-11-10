package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

// viewer is the image page: one asset of a timeline shown large, which the
// arrows beside it and the keyboard walk through.
type viewer struct {
	// feed is the timeline the page browses; id names the asset shown, and
	// dayKey the day the header names.
	feed   *feed
	id     string
	dayKey string
	// bm is the larger image once it loads; err why it did not.
	bm  *ui.Bitmap
	err error
	// attempt numbers the loads, so that one that finishes after the page
	// moved to another asset is dropped.
	attempt int
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
// walk the timeline too, and Escape returns to it.
func (a *app) photoPage(c *ui.Context, v *viewer) {
	stage := ui.Box(c).Fill().Background(ui.Hex("#0e0f13")).Clip().
		Focusable().AutoFocus().FocusRing(false)
	stage.Children(func() {
		switch {
		case v.err != nil:
			a.viewerError(c, v)
		case v.bm != nil:
			ui.Box(c).Fill().Padding(20).Children(func() {
				ui.Image(c, v.bm).Fill().Fit(ui.Contain)
			})
			if p := v.photo(); p != nil && p.Video {
				a.viewerVideoMark(c, p.Duration)
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
	})
	if stage.Shortcut(0, ui.KeyLeft) {
		a.viewerMove(-1)
	}
	if stage.Shortcut(0, ui.KeyRight) {
		a.viewerMove(1)
	}
	if stage.Shortcut(0, ui.KeyEscape) {
		a.closeViewer()
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
