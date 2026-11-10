package main

import (
	"log"
	"runtime"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// ownControls tells that the view draws the window controls itself, as
// kero does: on Windows and Linux the hidden title bar leaves none, while
// macOS keeps its traffic lights under a hidden inset title bar.
var ownControls = runtime.GOOS != "darwin"

// framed hides the window's title bar: under the traffic lights on macOS,
// without its buttons elsewhere, which windowControls draws in the view.
func framed(opts mygo.WindowOptions) mygo.WindowOptions {
	if ownControls {
		opts.TitleBarStyle = mygo.TitleBarHidden
		opts.HideWindowButtons = true
		// No menu bar over the sidebar and the card; Alt and F10 still
		// show the menus.
		opts.AutoHideMenuBar = true
		return opts
	}
	opts.TitleBarStyle = mygo.TitleBarHiddenInset
	opts.TitleBarHeight = topBar
	// The lights are 16 tall, and MyGo puts them a DIP above the position:
	// centered on barMid, as the sidebar's header and the card's are.
	opts.TrafficLightPosition = &mygo.Point{X: 18, Y: barMid - 7}
	return opts
}

// app is the state the window shows. Its view builds the interface from
// it, on the main thread, whenever the window needs a frame: after input,
// after Window.Update, and while something animates.
type app struct {
	win   *mygo.Window
	login loginState
	main  mainState
	// thumbs keeps the timeline's thumbnails within a memory budget.
	thumbs thumbCache
	// video plays the video the image page shows, and serves it to the
	// system's player.
	video videoState
}

func (a *app) view(c *ui.Context) {
	if a.login.stage == stageSignedIn {
		a.mainView(c)
	} else {
		a.login.view(c, a)
	}
	a.windowControls(c)
}

// update applies a change of the state on the main thread, then redraws
// the window. Checks started by connect run in a goroutine and publish
// their result through it.
func (a *app) update(fn func()) {
	if a.win == nil {
		// The tests run the view without a window.
		fn()
		return
	}
	a.win.Update(fn)
}

func main() {
	a := &app{}
	mygo.App.WhenReady(func() {
		a.prepareSession()
		// A saved session opens the window at the main view's size; the
		// login card stays small.
		opts := mygo.WindowOptions{
			Title:     "Immich Mygo",
			Width:     480,
			Height:    520,
			MinWidth:  460,
			MinHeight: 480,
			// Opens where the user left it last time.
			StateKey: "main",
			// The window shows the interface MyGo draws, not a web page.
			Content: ui.View(a.view),
		}
		if a.login.stage == stageRestoring {
			opts.Width, opts.Height = 1120, 720
			opts.MinWidth, opts.MinHeight = 720, 520
		}
		a.win = mygo.NewWindow(framed(opts))
		if a.login.stage == stageRestoring {
			go a.restoreSession()
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
