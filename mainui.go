package main

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
)

// sidebarWidth is how wide the sidebar opens, in DIPs.
const sidebarWidth = 248

// sidebarMotion is how the sidebar comes and goes, as kero's does: a
// curve that lands quickly, without a spring's crawl under a pixel.
var sidebarMotion = ui.Animation{Duration: 260 * time.Millisecond, Ease: ui.CubicBezier(0.25, 1, 0.5, 1)}

// section is a page the main window's sidebar leads to.
type section uint8

const (
	sectionPhotos section = iota
	sectionFavorites
	sectionAlbums
)

func (s section) title() string {
	switch s {
	case sectionFavorites:
		return "收藏"
	case sectionAlbums:
		return "相册"
	default:
		return "照片"
	}
}

// feed is what a section shows, and how loading it went.
type feed struct {
	// days are the timeline's days, newest first, their assets loading as
	// the section shows them; source says whose days they are.
	days    []*timelineDay
	source  timelineSource
	albums  []*album
	loading bool
	loaded  bool
	err     error
	// attempt numbers the loads, so that one that finishes after a newer
	// one started is dropped.
	attempt int
	grid    ui.GridState
	list    ui.ListState
}

// mainState is the main window: the section shown, and each section's
// content, loaded once and kept.
type mainState struct {
	section   section
	photos    feed
	favorites feed
	albums    feed
	// albumOpen is the album shown from the grid, and albumFeed the days
	// it loads into.
	albumOpen *album
	albumFeed feed
	// viewer is the image page shown in place of the timeline, or nil.
	viewer *viewer
	// sidebarHidden hides the sidebar, which the card's header then offers
	// to bring back. sidebarProgress is how far it shows this frame, 0 to
	// 1, which the header reads.
	sidebarHidden   bool
	sidebarProgress float32
}

func (m *mainState) current() *feed {
	switch m.section {
	case sectionFavorites:
		return &m.favorites
	case sectionAlbums:
		if m.albumOpen != nil {
			return &m.albumFeed
		}
		return &m.albums
	default:
		return &m.photos
	}
}

// The window's title bar is hidden (framed): these are where its contents
// sit, as kero measures its own. barMid is the line the sidebar's header,
// the card's header and the window controls center on.
const (
	barMid    = 30
	topBar    = 2 * barMid
	headerH   = 2 * (barMid - 8) // the card's header, clear of its edge
	controlsW = 3*28 + 2*2
)

// photoTileH is the height of a tile of the timeline, photoMinW the least
// width it takes, and photoGap the room between tiles: as many columns as
// fit across the card.
const (
	photoTileH = 128
	photoMinW  = 148
	photoGap   = 8
)

// The icons of the main window, on 16 as kero's, in the text's color.
var (
	photosIcon    = svgIcon(`<rect x="2" y="3" width="12" height="10" rx="2"/><circle cx="5.6" cy="6.4" r="1.1"/><path d="M3 11.6l3.2-3 2.4 2.2 1.7-1.5 2.7 2.3"/>`)
	favoritesIcon = svgIcon(`<path d="M8 13.1S2.6 9.8 2.6 6.2A3 3 0 0 1 8 4.3a3 3 0 0 1 5.4 1.9c0 3.6-5.4 6.9-5.4 6.9z"/>`)
	albumsIcon    = svgIcon(`<rect x="2.5" y="3.5" width="11" height="9" rx="2"/><path d="M2.5 10l3-2.4 2.4 1.9 2-1.4 3.6 2.4"/>`)
	refreshIcon   = svgIcon(`<path d="M13.2 8a5.2 5.2 0 1 1-1.5-3.7"/><path d="M13.2 2.7v2.8h-2.8"/>`)
	signOutIcon   = svgIcon(`<path d="M6.6 3H4.7A1.7 1.7 0 0 0 3 4.7v6.6A1.7 1.7 0 0 0 4.7 13h1.9"/><path d="M10.2 5.4L12.8 8l-2.6 2.6"/><path d="M6.6 8h6.2"/>`)
	errorIcon     = svgIcon(`<circle cx="8" cy="8" r="5.6"/><path d="M8 5.2v3.4"/><path d="M8 10.9v.1"/>`)
	sidebarIcon   = svgIcon(`<rect x="2" y="3" width="12" height="10" rx="2"/><path d="M6 3v10"/>`)
	backIcon      = svgIcon(`<path d="M9.8 3.5L5.3 8l4.5 4.5"/><path d="M5.5 8h7"/>`)
	forwardIcon   = svgIcon(`<path d="M6.2 3.5L10.7 8l-4.5 4.5"/><path d="M10.5 8h-7"/>`)
	minusIcon     = svgIcon(`<path d="M4 8h8"/>`)
	plusIcon      = svgIcon(`<path d="M8 4v8"/><path d="M4 8h8"/>`)
	infoIcon      = svgIcon(`<circle cx="8" cy="8" r="5.6"/><path d="M8 7.2v3.4"/><path d="M8 4.9v.1"/>`)
	playIcon      = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><path d="M5 3.4l7 4.6-7 4.6z" fill="currentColor"/></svg>`))

	minimizeIcon = svgIcon(`<path d="M3.5 8h9"/>`)
	maximizeIcon = svgIcon(`<rect x="3.5" y="3.5" width="9" height="9" rx="1.5"/>`)
	restoreIcon  = svgIcon(`<rect x="3.5" y="5.5" width="7" height="7" rx="1.2"/><path d="M5.5 3.5h5.5a1.5 1.5 0 0 1 1.5 1.5v5.5"/>`)
	closeIcon    = svgIcon(`<path d="M4 4l8 8"/><path d="M12 4l-8 8"/>`)
)

// svgIcon draws an icon of the app's own on 16, as a toolbar glyph.
func svgIcon(shapes string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round">` + shapes + `</svg>`))
}

// mainView is the window after a login: a sidebar on the left, and the
// section's card beside it, as kero draws its own. The sidebar slides
// away in whole pixels as it hides, the card taking its room.
func (a *app) mainView(c *ui.Context) {
	t := c.Theme()
	row := ui.Row(c).Fill().AlignItems(ui.Stretch)
	progress := row.AnimateTo("sidebar", b2f(!a.main.sidebarHidden), sidebarMotion)
	a.main.sidebarProgress = progress
	sideW := float32(math.Round(float64(sidebarWidth * min(max(progress, 0), 1))))
	row.Children(func() {
		ui.Row(c).Width(sideW).Shrink(0).AlignItems(ui.Stretch).ClipX().Children(func() {
			// The tint and the drag area belong to this column, which the
			// row stretches to the window's height: a column of its own
			// would be no taller than what the sidebar lists.
			ui.Column(c).Width(sidebarWidth).Shrink(0).Background(sidebarTint(t)).DragWindow().
				Padding(0, 10, 10, 10).Margin(0, 0, 0, sideW-sidebarWidth).
				Opacity(0.4 + 0.6*progress).Children(func() {
				a.mainSidebar(c)
			})
		})
		ui.Column(c).Grow(1).MinWidth(0).Padding(8).DragWindow().Children(func() {
			ui.Column(c).Grow(1).MinHeight(0).Radius(12).Background(cardFace(t)).
				Border(1, t.Border).Clip().Children(func() {
				a.cardHeader(c)
				a.cardBody(c)
			})
		})
	})
}

// b2f is 1 for true, 0 for false, as animations take it.
func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

// sidebarTint is the color under the sidebar, a shade off the card's, as
// kero washes its own.
func sidebarTint(t *ui.Theme) ui.Color {
	if t.Dark {
		return t.Background
	}
	return ui.Hex("#eceef4")
}

// cardFace is the card's fill: white in the light, the theme's surface in
// the dark, where it stands above the window.
func cardFace(t *ui.Theme) ui.Color {
	if t.Dark {
		return t.Surface
	}
	return t.Background
}

// rowHover is what a sidebar row's fill becomes under the pointer.
func rowHover(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.RGBA(255, 255, 255, 0.06)
	}
	return ui.RGBA(0, 0, 0, 0.045)
}

// mainSidebar lists where the app goes, and who is signed in, in the
// column mainView wraps.
func (a *app) mainSidebar(c *ui.Context) {
	t := c.Theme()
	bar := c.TitleBar()
	// The room the traffic lights take on macOS, before the header's own
	// edge; the window controls elsewhere sit over the card's header.
	lead := float32(6)
	if !ownControls {
		lead += bar.Left
	}
	ui.Row(c).Height(topBar).Padding(0, 2, 0, lead).Gap(10).AlignItems(ui.Center).DragWindow().Children(func() {
		ui.Box(c).Size(30, 30).Radius(8).Background(ui.Hex("#ffffff")).Center().Children(func() {
			if logo != nil {
				ui.Image(c, logo).Size(26, 26)
			} else {
				ui.Text(c, "I").FontSize(16).Bold().TextColor(t.Accent)
			}
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, "Immich Mygo").FontSize(14).FontWeight(600)
			ui.Text(c, serverName(a.login.serverURL)).FontSize(11).TextColor(t.TextMuted).
				SingleLine().Ellipsis("…")
		})
		a.iconButton(c, sidebarIcon, "隐藏边栏", a.toggleSidebar)
	})
	a.sideSection(c, sectionPhotos, photosIcon)
	a.sideSection(c, sectionFavorites, favoritesIcon)
	a.sideSection(c, sectionAlbums, albumsIcon)
	ui.Spacer(c)
	if a.login.storageErr != "" {
		ui.Text(c, a.login.storageErr).FontSize(11).TextColor(t.Warning).
			MaxWidth(220).Margin(0, 0, 8, 0)
	}
	a.sideUser(c)
}

// sideSection is a row of the sidebar: a page of the app.
func (a *app) sideSection(c *ui.Context, s section, icon *ui.SVG) {
	t := c.Theme()
	selected := a.main.section == s
	row := ui.Row(c).Height(34).Padding(0, 10).Gap(9).Radius(7).AlignItems(ui.Center).Label(s.title())
	switch {
	case selected:
		row.Background(cardFace(t)).Border(1, t.Border)
	case row.Hovered():
		row.Background(rowHover(t))
	}
	fg := t.TextMuted
	if selected {
		fg = t.Text
	}
	row.Children(func() {
		iconFg := fg
		if selected {
			iconFg = t.Accent
		}
		ui.Icon(c, icon).Size(15, 15).TextColor(iconFg)
		ui.Text(c, s.title()).FontSize(13.5).TextColor(fg)
	})
	if row.Clicked() {
		a.selectSection(s)
	}
}

// sideUser shows who is signed in, and signs them out.
func (a *app) sideUser(c *ui.Context) {
	t := c.Theme()
	name, email := "你", ""
	if a.login.user != nil {
		if a.login.user.Name != "" {
			name = a.login.user.Name
		}
		email = a.login.user.Email
	}
	ui.Row(c).Height(42).Padding(0, 6).Gap(9).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(28, 28).Radius(14).Background(t.Accent).Center().Children(func() {
			ui.Text(c, initial(name, email)).FontSize(12).Bold().TextColor(t.AccentText)
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, name).FontSize(12.5).SingleLine().Ellipsis("…")
			if email != "" {
				ui.Text(c, email).FontSize(10.5).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
			}
		})
		b := ui.ButtonBase(c).Size(26, 26).Radius(6).Center().Label("退出登录").Tooltip("退出登录")
		if b.Pressed() {
			b.Background(t.SurfacePressed)
		} else if b.Hovered() {
			b.Background(t.SurfaceHover)
		}
		b.Children(func() { ui.Icon(c, signOutIcon).Size(15, 15).TextColor(t.TextMuted) })
		if b.Clicked() {
			a.signOut()
		}
	})
}

// cardHeader names the section shown, and offers to load it again. It is
// the window's title bar too, and keeps room at its right for the window
// controls over it. As the sidebar hides, the button that brings it back
// comes at its left, and the title makes room. An album open from the grid
// names itself here, with the way back to the grid, and the image page
// names its day, with the way back to the timeline.
func (a *app) cardHeader(c *ui.Context) {
	t := c.Theme()
	p := a.main.sidebarProgress
	pad := 18 + 34*(1-p)
	viewer := a.main.viewer
	viewerOpen := viewer != nil
	album := a.main.albumOpen
	albumOpen := !viewerOpen && a.main.section == sectionAlbums && album != nil
	h := ui.Row(c).Height(headerH).Shrink(0).Padding(0, 12, 0, pad).Gap(8).AlignItems(ui.Center).DragWindow()
	h.Children(func() {
		if p < 0.5 {
			ui.Row(c).Absolute().Left(6).Top(0).Bottom(0).AlignItems(ui.Center).
				Opacity(1 - 2*p).Children(func() {
				a.iconButton(c, sidebarIcon, "显示边栏", a.toggleSidebar)
			})
		}
		title := a.main.section.title()
		var week, count string
		switch {
		case viewerOpen:
			a.iconButton(c, backIcon, "返回时间线", a.closeViewer)
			title, week = a.viewerCaption()
			count = a.viewerCounter()
		case albumOpen:
			a.iconButton(c, backIcon, "返回相册", a.closeAlbum)
			title = album.Name
			if n := a.albumCount(); n > 0 {
				count = fmt.Sprintf("%d 项", n)
			}
		}
		ui.Text(c, title).FontSize(15).FontWeight(600).
			SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
		if week != "" {
			ui.Text(c, week).FontSize(11.5).TextColor(t.TextMuted)
		}
		if count != "" {
			ui.Text(c, count).FontSize(11.5).TextColor(t.TextMuted)
		}
		ui.Spacer(c)
		if viewerOpen {
			a.detailsButton(c, viewer.showDetails)
		} else {
			a.iconButton(c, refreshIcon, "刷新", a.refreshSection)
		}
		if ownControls {
			ui.Box(c).Width(controlsW + 6)
		}
	})
}

// albumCount is how many assets the open album holds: what the grid knew
// of it, or the days as they load.
func (a *app) albumCount() int {
	f := &a.main.albumFeed
	if !f.loaded {
		if a.main.albumOpen != nil {
			return a.main.albumOpen.Count
		}
		return 0
	}
	n := 0
	for _, d := range f.days {
		n += d.count
	}
	return n
}

// toggleSidebar hides the sidebar, or brings it back.
func (a *app) toggleSidebar() {
	a.main.sidebarHidden = !a.main.sidebarHidden
}

// iconButton is a quiet square button with an icon, as in a toolbar.
func (a *app) iconButton(c *ui.Context, icon *ui.SVG, label string, fn func()) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c).Size(28, 26).Radius(6).Center().Label(label).Tooltip(label)
	if b.Pressed() {
		b.Background(t.SurfacePressed)
	} else if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	b.Children(func() { ui.Icon(c, icon).Size(15, 15).TextColor(t.TextMuted) })
	if b.Clicked() {
		fn()
	}
	return b
}

// windowControls are the window's minimize, maximize and close buttons,
// which the hidden title bar leaves the view to draw (ownControls), as
// kero draws its own: over the card's header in the main view, and at the
// login window's top right.
func (a *app) windowControls(c *ui.Context) {
	if !ownControls {
		return
	}
	t := c.Theme()
	top, right := float32(barMid-13), float32(18)
	if a.login.stage != stageSignedIn {
		top, right = 10, 10
	}
	ui.Row(c).Absolute().Right(right).Top(top).Gap(2).AlignItems(ui.Center).DragWindow().Children(func() {
		a.controlButtons(c, t)
	})
}

// controlButtons are the window controls' three buttons.
func (a *app) controlButtons(c *ui.Context, t *ui.Theme) {
	minimize := a.controlButton(c, t, minimizeIcon, "最小化")
	if minimize.Clicked() && a.win != nil {
		a.win.Minimize()
	}

	maximized := a.win != nil && a.win.IsMaximized()
	icon, label := maximizeIcon, "最大化"
	if maximized {
		icon, label = restoreIcon, "还原"
	}
	maximize := a.controlButton(c, t, icon, label)
	// The system's maximize button for the pointer, which brings Windows
	// 11's snap layouts as it rests there.
	maximize.MaximizeButton()
	if maximize.Clicked() && a.win != nil {
		if maximized {
			a.win.Unmaximize()
		} else {
			a.win.Maximize()
		}
	}

	close := ui.ButtonBase(c).Size(28, 26).Radius(6).Center().Label("关闭窗口").Tooltip("关闭窗口")
	fg := t.TextMuted
	if close.Hovered() || close.Pressed() {
		close.Background(t.Danger)
		fg = ui.Hex("#ffffff")
	}
	close.Children(func() { ui.Icon(c, closeIcon).Size(15, 15).TextColor(fg) })
	if close.Clicked() && a.win != nil {
		a.win.Close()
	}
}

// controlButton is one of the window's own buttons: quiet, and lighting
// under the pointer.
func (a *app) controlButton(c *ui.Context, t *ui.Theme, icon *ui.SVG, label string) ui.Element {
	b := ui.ButtonBase(c).Size(28, 26).Radius(6).Center().Label(label).Tooltip(label)
	fg := t.TextMuted
	if b.Pressed() {
		b.Background(t.SurfacePressed)
		fg = t.Text
	} else if b.Hovered() {
		b.Background(t.SurfaceHover)
		fg = t.Text
	}
	b.Children(func() { ui.Icon(c, icon).Size(15, 15).TextColor(fg) })
	return b
}

// cardBody shows the section: its days, its albums, the image page, or how
// loading them went.
func (a *app) cardBody(c *ui.Context) {
	if v := a.main.viewer; v != nil {
		a.photoPage(c, v)
		return
	}
	f := a.main.current()
	switch {
	case f.err != nil:
		title, action, fn := "加载失败", "重试", a.refreshSection
		if errors.Is(f.err, errSessionExpired) {
			title, action, fn = "登录已过期", "重新登录", a.signOut
		}
		a.emptyState(c, errorIcon, title, f.err.Error(), action, fn)
	case !f.loaded && !f.loading:
		a.emptyState(c, photosIcon, "还没有内容", "", "刷新", a.refreshSection)
	case !f.loaded:
		a.loadingState(c)
	case a.main.section == sectionAlbums:
		if a.main.albumOpen != nil {
			if len(f.days) == 0 {
				a.emptyState(c, albumsIcon, "相册是空的", "在 Immich 里向相册添加照片后，它们会出现在这里。", "刷新", a.refreshSection)
				return
			}
			a.timeline(c, f)
			return
		}
		if len(f.albums) == 0 {
			a.emptyState(c, albumsIcon, "还没有相册", "在 Immich 里创建相册后，它们会出现在这里。", "刷新", a.refreshSection)
			return
		}
		a.albumGrid(c, f)
	case len(f.days) == 0:
		title, detail := "还没有照片", "上传照片到 Immich 后，它们会出现在这里。"
		if a.main.section == sectionFavorites {
			title, detail = "还没有收藏", "在 Immich 里把照片设为收藏，它们会出现在这里。"
		}
		a.emptyState(c, favoritesIcon, title, detail, "刷新", a.refreshSection)
	default:
		a.timeline(c, f)
	}
}

// loadingState shows that the section is on its way.
func (a *app) loadingState(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Center().Gap(10).Children(func() {
		ui.Spinner(c).Label("正在加载")
		ui.Text(c, "正在加载…").FontSize(12).TextColor(t.TextMuted)
	})
}

// emptyState centers a message in the card, with an action.
func (a *app) emptyState(c *ui.Context, icon *ui.SVG, title, detail, action string, fn func()) {
	t := c.Theme()
	ui.Column(c).Fill().Center().Gap(6).Padding(24).Children(func() {
		ui.Box(c).Size(48, 48).Radius(12).Background(t.SurfaceHover).Center().
			Margin(0, 0, 8, 0).Children(func() {
			ui.Icon(c, icon).Size(22, 22).TextColor(t.TextMuted)
		})
		ui.Text(c, title).FontSize(17).FontWeight(600)
		if detail != "" {
			ui.Text(c, detail).FontSize(12.5).TextColor(t.TextMuted).
				MaxWidth(400).TextAlign(ui.Center)
		}
		ui.Box(c).Height(6)
		ui.PrimaryButton(c, action).OnClick(fn)
	})
}

// timeline shows the section's days: a heading a day, its photos under it
// in rows the card fits, and the heading held at the top as they scroll
// under it. The days the list shows load as they come into view.
func (a *app) timeline(c *ui.Context, f *feed) {
	cols := a.timelineCols(c)
	starts := make([]int, len(f.days)+1)
	for i, d := range f.days {
		starts[i+1] = starts[i] + timelineDayRows(d, cols)
	}
	dayAt := func(row int) (int, int) {
		i := sort.Search(len(f.days), func(i int) bool { return starts[i+1] > row })
		i = min(i, len(f.days)-1)
		return i, row - starts[i]
	}
	f.list.Header = func(row int) bool {
		_, k := dayAt(row)
		return k == 0
	}
	f.list.Key = func(row int) any {
		i, k := dayAt(row)
		return timelineRowKey{day: f.days[i].key, row: k}
	}
	ui.List(c, &f.list, starts[len(starts)-1], func(row int) {
		i, k := dayAt(row)
		d := f.days[i]
		if k == 0 {
			a.dayHeading(c, f, i, d)
			return
		}
		n := d.count
		if d.loaded {
			n = len(d.photos)
		}
		from := (k - 1) * cols
		to := min(from+cols, n)
		ui.Grid(c).Columns(cols).Gap(photoGap).Margin(0, 0, photoGap, 0).Children(func() {
			for j := from; j < to; j++ {
				a.photoTile(c, f, i, d, j)
			}
		})
	}).Grow(1).Padding(12)
	a.loadShownDays(f, starts)
}

// timelineRowKey tells the rows of the timeline apart: the day they show,
// and the row within it.
type timelineRowKey struct {
	day string
	row int
}

// timelineDayRows is how many rows a day takes: its heading, and a row per
// cols photos.
func timelineDayRows(d *timelineDay, cols int) int {
	n := d.count
	if d.loaded {
		n = len(d.photos)
	}
	return 1 + (n+cols-1)/cols
}

// timelineCols is how many photos the card fits across: the window less
// the sidebar, the card's edge and padding, and the list's.
func (a *app) timelineCols(c *ui.Context) int {
	winW, _ := c.Size()
	p := math.Min(math.Max(float64(a.main.sidebarProgress), 0), 1)
	sideW := math.Round(sidebarWidth * p)
	avail := float64(winW) - sideW - 2*8 - 2 - 2*12
	cols := int((avail + photoGap) / (photoMinW + photoGap))
	return max(cols, 1)
}

// dayHeading heads a day of the timeline: its date and weekday, and what
// it holds. It stays at the top of the list while its day scrolls under
// it.
func (a *app) dayHeading(c *ui.Context, f *feed, i int, d *timelineDay) {
	t := c.Theme()
	title, week := bucketTitle(d.key, time.Now())
	h := ui.Row(c).Height(40).AlignItems(ui.Center).Gap(8).Background(cardFace(t))
	h.Children(func() {
		ui.Text(c, title).FontSize(14).FontWeight(600)
		if week != "" {
			ui.Text(c, week).FontSize(11.5).TextColor(t.TextMuted)
		}
		ui.Spacer(c)
		switch {
		case d.err != nil:
			ui.Text(c, "加载失败，点击重试").FontSize(11.5).TextColor(t.Danger)
		case d.loading && !d.loaded:
			ui.Text(c, "正在加载…").FontSize(11.5).TextColor(t.TextMuted)
		default:
			ui.Text(c, fmt.Sprintf("%d 项", d.count)).FontSize(11.5).TextColor(t.TextMuted)
		}
	})
	if d.err != nil && h.Clicked() {
		d.err = nil
		a.loadDay(f, i)
	}
}

// photoTile is one photo of the timeline, its thumbnail once it loads, and
// for a video how long it runs. A click opens it on the image page.
func (a *app) photoTile(c *ui.Context, f *feed, day int, d *timelineDay, j int) {
	t := c.Theme()
	tile := ui.Box(c).Height(photoTileH).Radius(8).Clip().Background(t.SurfaceHover)
	var p *photo
	if d.loaded && j < len(d.photos) {
		p = d.photos[j]
	}
	if p != nil {
		label := fmt.Sprintf("照片 %d", j+1)
		if p.Video {
			label = fmt.Sprintf("视频 %d", j+1)
		}
		tile.Label(label).Tooltip("查看").Cursor(ui.CursorPointer)
		// Only what the timeline shows has a thumbnail, and the cache
		// keeps what it shows lately.
		switch {
		case p.Bm != nil:
			a.thumbs.touch(p)
		case !p.loading && !p.failed:
			a.requestThumbnail(f, p)
		}
	}
	switch {
	case p != nil && p.Bm != nil:
		tile.Children(func() {
			ui.Image(c, p.Bm).Fill().Fit(ui.Cover)
			if p.Video {
				a.videoBadge(c, p.Duration)
			}
		})
	case p != nil && p.Video:
		tile.Center().Children(func() {
			ui.Icon(c, playIcon).Size(20, 20).TextColor(t.TextMuted)
		})
	default:
		tile.Center().Children(func() {
			ui.Icon(c, photosIcon).Size(20, 20).TextColor(t.TextMuted)
		})
	}
	if p != nil && tile.Clicked() {
		a.openPhoto(f, day, j)
	}
}

// videoBadge marks a video tile, and how long it runs.
func (a *app) videoBadge(c *ui.Context, ms int) {
	ui.Row(c).Absolute().Left(6).Bottom(6).Height(20).Padding(0, 6).Gap(4).
		Radius(10).Background(ui.RGBA(0, 0, 0, 0.55)).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, playIcon).Size(9, 9).TextColor(ui.Hex("#ffffff"))
		if ms > 0 {
			ui.Text(c, durationLabel(ms)).FontSize(10.5).TextColor(ui.Hex("#ffffff"))
		}
	})
}

// loadShownDays loads the days the timeline shows, and those just past its
// end, so that a scroll finds them ready.
func (a *app) loadShownDays(f *feed, starts []int) {
	first, last := f.list.Visible()
	if last < first {
		// Nothing laid out yet: the top of the timeline shows.
		a.loadDay(f, 0)
		return
	}
	ahead := last + 8
	for i := range f.days {
		if starts[i] > ahead {
			break
		}
		if starts[i+1] <= first {
			continue
		}
		a.loadDay(f, i)
	}
}

// loadDay loads one day of the timeline off the UI thread, publishing it
// through app.update. Its thumbnails load as the tiles show, not with the
// day.
func (a *app) loadDay(f *feed, i int) {
	if i < 0 || i >= len(f.days) || a.login.user == nil || a.login.user.Token == "" {
		return
	}
	d := f.days[i]
	if d.loaded || d.loading || d.err != nil {
		return
	}
	d.loading = true
	attempt := f.attempt
	base, token, src := a.login.serverURL, a.login.user.Token, f.source
	go func() {
		photos, err := fetchDayAssets(base, token, d.key, src)
		a.update(func() {
			if attempt != f.attempt {
				return
			}
			d.loading = false
			if err != nil {
				d.err = err
				if errors.Is(err, errSessionExpired) {
					f.err = err
				}
				return
			}
			d.loaded = true
			d.count = len(photos)
			d.photos = photos
		})
	}()
}

// albumGrid shows the section's albums: a cover, a name and its count.
// A click opens the album's days.
func (a *app) albumGrid(c *ui.Context, f *feed) {
	t := c.Theme()
	ui.GridView(c, &f.grid, len(f.albums), 168, 176, func(i int) {
		al := f.albums[i]
		cell := ui.Column(c).Fill().Gap(4).Label(al.Name).Cursor(ui.CursorPointer)
		cell.Children(func() {
			cover := ui.Box(c).Grow(1).MinHeight(0).Radius(10).Clip().Background(t.SurfaceHover)
			if al.Bm != nil {
				cover.Children(func() { ui.Image(c, al.Bm).Fill().Fit(ui.Cover) })
			} else {
				cover.Center().Children(func() {
					ui.Icon(c, albumsIcon).Size(20, 20).TextColor(t.TextMuted)
				})
			}
			ui.Text(c, al.Name).FontSize(12.5).FontWeight(500).SingleLine().Ellipsis("…")
			ui.Text(c, fmt.Sprintf("%d 项", al.Count)).FontSize(11).TextColor(t.TextMuted)
		})
		if cell.Clicked() {
			a.openAlbum(al)
		}
	}).Grow(1).Padding(12)
}

// openAlbum shows an album's days in place of the grid. The album last
// open keeps its days, which show at once.
func (a *app) openAlbum(al *album) {
	a.main.albumOpen = al
	f := &a.main.albumFeed
	if f.source.album == al.ID && (f.loaded || f.loading) {
		return
	}
	// The loads of the last album go on numbering, so that one finishing
	// late is not taken for the new album's.
	attempt := f.attempt
	*f = feed{source: timelineSource{album: al.ID}, attempt: attempt}
	a.loadTimeline(f)
}

// closeAlbum returns to the grid of albums, keeping the album's days for
// when it opens again.
func (a *app) closeAlbum() {
	a.main.albumOpen = nil
}

// selectSection shows another section, loading it the first time. Asking
// for the section shown again leaves the image page, or an open album for
// the grid.
func (a *app) selectSection(s section) {
	if a.main.section == s {
		if a.main.viewer != nil {
			a.closeViewer()
			return
		}
		if s == sectionAlbums && a.main.albumOpen != nil {
			a.closeAlbum()
		}
		return
	}
	a.closeViewer()
	if s != sectionAlbums {
		a.main.albumOpen = nil
	}
	a.main.section = s
	f := a.main.current()
	if f.loaded || f.loading {
		return
	}
	if s == sectionAlbums {
		a.loadAlbums(f)
		return
	}
	f.source = timelineSource{favorites: s == sectionFavorites}
	a.loadTimeline(f)
}

// refreshSection loads the section shown again.
func (a *app) refreshSection() {
	f := a.main.current()
	if f.loading {
		return
	}
	f.err = nil
	if a.main.section == sectionAlbums && a.main.albumOpen == nil {
		a.loadAlbums(f)
		return
	}
	a.loadTimeline(f)
}

// enterMain shows the main window after a login, with the photos of the
// server, growing the window from the login card's size.
func (a *app) enterMain() {
	a.main = mainState{}
	a.thumbs.clear()
	if a.win != nil {
		a.win.SetMinimumSize(720, 520)
		if w, h := a.win.Size(); w < 800 || h < 560 {
			a.win.SetSize(1120, 720)
		}
	}
	a.loadTimeline(&a.main.photos)
}

// loadAlbums loads the albums of the grid off the UI thread, publishing
// them through app.update, and their covers after them.
func (a *app) loadAlbums(f *feed) {
	if f.loading || a.login.user == nil || a.login.user.Token == "" {
		return
	}
	f.loading = true
	f.err = nil
	f.attempt++
	attempt := f.attempt
	base, token := a.login.serverURL, a.login.user.Token
	go func() {
		albums, err := fetchAlbums(base, token)
		a.update(func() {
			if attempt != f.attempt {
				return
			}
			f.loading = false
			if err != nil {
				f.err = err
				return
			}
			f.loaded = true
			f.albums = albums
		})
		if err != nil {
			return
		}
		a.loadAlbumCovers(f, attempt, base, token, albums)
	}()
}

// loadTimeline loads a timeline off the UI thread, the user's photos,
// their favorites or an album's days, publishing what it finds through
// app.update. A load that finishes after another started is dropped.
func (a *app) loadTimeline(f *feed) {
	if f.loading || a.login.user == nil || a.login.user.Token == "" {
		return
	}
	f.loading = true
	f.err = nil
	f.attempt++
	attempt := f.attempt
	base, token, src := a.login.serverURL, a.login.user.Token, f.source
	go func() {
		days, err := fetchTimelineDays(base, token, src)
		a.update(func() {
			if attempt != f.attempt {
				return
			}
			f.loading = false
			if err != nil {
				f.err = err
				return
			}
			f.loaded = true
			f.days = days
			// The top of the timeline comes first, before the list can
			// tell what it shows.
			a.loadDay(f, 0)
		})
	}()
}

// requestThumbnail loads one photo's thumbnail, for the tile that shows it.
// Only a few load at once; the tiles the budget waits on ask again on a
// later frame.
func (a *app) requestThumbnail(f *feed, p *photo) {
	if a.login.user == nil || a.login.user.Token == "" {
		return
	}
	select {
	case thumbLoads <- struct{}{}:
	default:
		return
	}
	p.loading = true
	attempt := f.attempt
	base, token := a.login.serverURL, a.login.user.Token
	go func() {
		defer func() { <-thumbLoads }()
		bm, err := fetchThumbnail(base, token, p.ID)
		a.update(func() {
			p.loading = false
			if attempt != f.attempt {
				return
			}
			if err != nil {
				p.failed = true
				return
			}
			p.Bm = bm
			a.thumbs.put(p)
		})
	}()
}

// loadAlbumCovers loads the covers of the albums, four at a time.
func (a *app) loadAlbumCovers(f *feed, attempt int, base, token string, albums []*album) {
	var wg sync.WaitGroup
	workers := make(chan struct{}, 4)
	for _, al := range albums {
		if al.Bm != nil || al.Cover == "" {
			continue
		}
		wg.Add(1)
		workers <- struct{}{}
		go func(al *album) {
			defer wg.Done()
			defer func() { <-workers }()
			bm, err := fetchThumbnail(base, token, al.Cover)
			if err != nil {
				return
			}
			a.update(func() {
				if attempt != f.attempt {
					return
				}
				al.Bm = bm
			})
		}(al)
	}
	wg.Wait()
}

// serverName is the address of the server as the sidebar shows it.
func serverName(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return u.Host
}

// initial is the letter of the user's avatar: the first of their name, or
// else of their email.
func initial(name, email string) string {
	for _, s := range []string{name, email} {
		r := []rune(strings.TrimSpace(s))
		if len(r) > 0 {
			return strings.ToUpper(string(r[0]))
		}
	}
	return "?"
}
