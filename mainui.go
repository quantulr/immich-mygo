package main

import (
	"errors"
	"fmt"
	"math"
	"net/url"
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
	assets  []*photo
	albums  []*album
	loading bool
	loaded  bool
	err     error
	// attempt numbers the loads, so that one that finishes after a newer
	// one started is dropped.
	attempt int
	grid    ui.GridState
}

// mainState is the main window: the section shown, and each section's
// content, loaded once and kept.
type mainState struct {
	section   section
	photos    feed
	favorites feed
	albums    feed
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

// The icons of the main window, on 16 as kero's, in the text's color.
var (
	photosIcon    = svgIcon(`<rect x="2" y="3" width="12" height="10" rx="2"/><circle cx="5.6" cy="6.4" r="1.1"/><path d="M3 11.6l3.2-3 2.4 2.2 1.7-1.5 2.7 2.3"/>`)
	favoritesIcon = svgIcon(`<path d="M8 13.1S2.6 9.8 2.6 6.2A3 3 0 0 1 8 4.3a3 3 0 0 1 5.4 1.9c0 3.6-5.4 6.9-5.4 6.9z"/>`)
	albumsIcon    = svgIcon(`<rect x="2.5" y="3.5" width="11" height="9" rx="2"/><path d="M2.5 10l3-2.4 2.4 1.9 2-1.4 3.6 2.4"/>`)
	refreshIcon   = svgIcon(`<path d="M13.2 8a5.2 5.2 0 1 1-1.5-3.7"/><path d="M13.2 2.7v2.8h-2.8"/>`)
	signOutIcon   = svgIcon(`<path d="M6.6 3H4.7A1.7 1.7 0 0 0 3 4.7v6.6A1.7 1.7 0 0 0 4.7 13h1.9"/><path d="M10.2 5.4L12.8 8l-2.6 2.6"/><path d="M6.6 8h6.2"/>`)
	errorIcon     = svgIcon(`<circle cx="8" cy="8" r="5.6"/><path d="M8 5.2v3.4"/><path d="M8 10.9v.1"/>`)
	sidebarIcon   = svgIcon(`<rect x="2" y="3" width="12" height="10" rx="2"/><path d="M6 3v10"/>`)

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
			ui.Text(c, "Immich").FontSize(14).FontWeight(600)
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
// comes at its left, and the title makes room.
func (a *app) cardHeader(c *ui.Context) {
	p := a.main.sidebarProgress
	pad := 18 + 34*(1-p)
	h := ui.Row(c).Height(headerH).Shrink(0).Padding(0, 12, 0, pad).Gap(8).AlignItems(ui.Center).DragWindow()
	h.Children(func() {
		if p < 0.5 {
			ui.Row(c).Absolute().Left(6).Top(0).Bottom(0).AlignItems(ui.Center).
				Opacity(1 - 2*p).Children(func() {
				a.iconButton(c, sidebarIcon, "显示边栏", a.toggleSidebar)
			})
		}
		ui.Text(c, a.main.section.title()).FontSize(15).FontWeight(600)
		ui.Spacer(c)
		a.iconButton(c, refreshIcon, "刷新", a.refreshSection)
		if ownControls {
			ui.Box(c).Width(controlsW + 6)
		}
	})
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

// cardBody shows the section: its photos, its albums, or how loading them
// went.
func (a *app) cardBody(c *ui.Context) {
	t := c.Theme()
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
	case f.loading && len(f.assets) == 0 && len(f.albums) == 0:
		ui.Column(c).Fill().Center().Gap(10).Children(func() {
			ui.Spinner(c).Label("正在加载")
			ui.Text(c, "正在加载…").FontSize(12).TextColor(t.TextMuted)
		})
	case a.main.section == sectionAlbums:
		if len(f.albums) == 0 {
			a.emptyState(c, albumsIcon, "还没有相册", "在 Immich 里创建相册后，它们会出现在这里。", "刷新", a.refreshSection)
			return
		}
		a.albumGrid(c, f)
	case len(f.assets) == 0:
		title, detail := "还没有照片", "上传照片到 Immich 后，它们会出现在这里。"
		if a.main.section == sectionFavorites {
			title, detail = "还没有收藏", "在 Immich 里把照片设为收藏，它们会出现在这里。"
		}
		a.emptyState(c, favoritesIcon, title, detail, "刷新", a.refreshSection)
	default:
		a.photoGrid(c, f)
	}
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

// photoGrid shows the section's photos, a square each, filling as their
// thumbnails load.
func (a *app) photoGrid(c *ui.Context, f *feed) {
	t := c.Theme()
	ui.GridView(c, &f.grid, len(f.assets), 148, 128, func(i int) {
		p := f.assets[i]
		tile := ui.Box(c).Fill().Radius(8).Clip().Background(t.SurfaceHover)
		if p.Bm != nil {
			tile.Children(func() { ui.Image(c, p.Bm).Fill().Fit(ui.Cover) })
		} else {
			tile.Center().Children(func() {
				ui.Icon(c, photosIcon).Size(20, 20).TextColor(t.TextMuted)
			})
		}
	}).Grow(1).Padding(12)
}

// albumGrid shows the section's albums: a cover, a name and its count.
func (a *app) albumGrid(c *ui.Context, f *feed) {
	t := c.Theme()
	ui.GridView(c, &f.grid, len(f.albums), 168, 176, func(i int) {
		al := f.albums[i]
		ui.Column(c).Fill().Gap(4).Children(func() {
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
	}).Grow(1).Padding(12)
}

// selectSection shows another section, loading it the first time.
func (a *app) selectSection(s section) {
	if a.main.section == s {
		return
	}
	a.main.section = s
	if f := a.main.current(); !f.loaded && !f.loading {
		a.loadFeed(f, s)
	}
}

// refreshSection loads the section shown again.
func (a *app) refreshSection() {
	f := a.main.current()
	if f.loading {
		return
	}
	f.err = nil
	a.loadFeed(f, a.main.section)
}

// enterMain shows the main window after a login, with the photos of the
// server, growing the window from the login card's size.
func (a *app) enterMain() {
	a.main = mainState{}
	if a.win != nil {
		a.win.SetMinimumSize(720, 520)
		if w, h := a.win.Size(); w < 800 || h < 560 {
			a.win.SetSize(1120, 720)
		}
	}
	a.loadFeed(&a.main.photos, sectionPhotos)
}

// loadFeed loads the content of a section off the UI thread, publishing
// what it finds through app.update. A load that finishes after another
// started is dropped.
func (a *app) loadFeed(f *feed, s section) {
	if f.loading || a.login.user == nil || a.login.user.Token == "" {
		return
	}
	f.loading = true
	f.err = nil
	f.attempt++
	attempt := f.attempt
	base, token := a.login.serverURL, a.login.user.Token

	if s == sectionAlbums {
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
		return
	}

	favorites := s == sectionFavorites
	go func() {
		assets, err := fetchTimeline(base, token, favorites, feedLimit)
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
			f.assets = assets
		})
		if err != nil {
			return
		}
		a.loadThumbnails(f, attempt, base, token, assets)
	}()
}

// loadThumbnails loads the photos' thumbnails, four at a time, so that
// the grid fills as they come.
func (a *app) loadThumbnails(f *feed, attempt int, base, token string, assets []*photo) {
	var wg sync.WaitGroup
	workers := make(chan struct{}, 4)
	for _, p := range assets {
		if p.Bm != nil {
			continue
		}
		wg.Add(1)
		workers <- struct{}{}
		go func(p *photo) {
			defer wg.Done()
			defer func() { <-workers }()
			bm, err := fetchThumbnail(base, token, p.ID)
			if err != nil {
				return
			}
			a.update(func() {
				if attempt != f.attempt {
					return
				}
				p.Bm = bm
			})
		}(p)
	}
	wg.Wait()
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
