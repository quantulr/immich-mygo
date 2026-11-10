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
}

func (a *app) view(c *ui.Context) {
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
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:     "Immich",
			Width:     480,
			Height:    520,
			MinWidth:  460,
			MinHeight: 480,
			// Opens where the user left it last time.
			StateKey: "login",
			// The window shows the interface MyGo draws, not a web page.
			Content: ui.View(a.view),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
