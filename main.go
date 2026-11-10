package main

import (
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// app is the state the window shows. Its view builds the interface from
// it, on the main thread, whenever the window needs a frame: after input,
// after Window.Update, and while something animates.
type app struct {
	win   *mygo.Window
	login loginState
	main  mainState
}

func (a *app) view(c *ui.Context) {
	if a.login.stage == stageSignedIn {
		a.mainView(c)
		return
	}
	a.login.view(c, a)
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
			Title:     "Immich",
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
		a.win = mygo.NewWindow(opts)
		if a.login.stage == stageRestoring {
			go a.restoreSession()
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
