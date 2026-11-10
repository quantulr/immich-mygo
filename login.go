package main

import (
	_ "embed"

	"github.com/egoist/mygo/ui"
)

//go:embed resources/icon.png
var iconPNG []byte

// logo is the app's icon, decoded once. It is nil if the icon cannot be
// decoded, and the card shows a letter instead.
var logo = func() *ui.Bitmap {
	b, err := ui.DecodeBitmap(iconPNG)
	if err != nil {
		return nil
	}
	return b
}()

// loginState is the login page: the server address the user types, and
// what the last check of it found.
type loginState struct {
	serverURL string
	checking  bool
	err       string
	version   string
	// attempt numbers the checks, so that one that finishes after the
	// address changed is dropped.
	attempt int
}

func (l *loginState) view(c *ui.Context, a *app) {
	t := c.Theme()
	ui.Column(c).Fill().Center().Padding(24).Children(func() {
		ui.Column(c).Width(400).Padding(28).Gap(22).Radius(10).
			Background(t.Surface).Border(1, t.Border).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(44, 44).Radius(12).Background(ui.Hex("#ffffff")).Center().Children(func() {
					if logo != nil {
						ui.Image(c, logo).Size(36, 36)
					} else {
						ui.Text(c, "I").FontSize(22).Bold().TextColor(t.Accent)
					}
				})
				ui.Column(c).Gap(2).Children(func() {
					ui.Text(c, "Immich").FontSize(22).Bold()
					ui.Text(c, "第三方桌面客户端").FontSize(12).TextColor(t.TextMuted)
				})
			})
			ui.Field(c, "服务器地址", func() {
				ui.TextInput(c, &l.serverURL).
					Placeholder("https://photos.example.com").
					AutoFocus().
					OnChange(a.serverURLChanged).
					OnSubmit(a.connect)
			}).Description("可省略 https://，带 /api 的地址也可以。").
				Error(l.err)
			ui.Column(c).Gap(10).Children(func() {
				ui.PrimaryButton(c, "连接").Disabled(l.checking).OnClick(a.connect)
				l.status(c)
			})
		})
	})
}

// status shows what the last check found: that it runs, or the server it
// reached. Errors show under the field instead (Field.Error).
func (l *loginState) status(c *ui.Context) {
	t := c.Theme()
	switch {
	case l.checking:
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Label("正在检查服务器")
			ui.Text(c, "正在检查服务器…").FontSize(12).TextColor(t.TextMuted)
		})
	case l.version != "":
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(8, 8).Radius(4).Background(t.Success)
			ui.Text(c, "已连接到 Immich "+l.version).FontSize(12).TextColor(t.Success)
		})
	}
}

// serverURLChanged forgets what the check of the previous address found,
// and drops its result if it is still running.
func (a *app) serverURLChanged() {
	l := &a.login
	l.attempt++
	l.checking = false
	l.err = ""
	l.version = ""
}

// connect checks the server the user typed: its Immich ping, and the
// version it runs. Slow work runs off the UI thread and publishes through
// app.update.
func (a *app) connect() {
	l := &a.login
	if l.checking {
		return
	}
	base, err := normalizeServerURL(l.serverURL)
	if err != nil {
		l.err = err.Error()
		l.version = ""
		return
	}
	// Show the address in its canonical form while checking it.
	l.serverURL = base
	l.checking = true
	l.err = ""
	l.version = ""
	l.attempt++
	attempt := l.attempt
	go func() {
		info, err := pingImmich(base)
		a.update(func() {
			if attempt != l.attempt {
				// The address changed while the check ran.
				return
			}
			l.checking = false
			if err != nil {
				l.err = err.Error()
				return
			}
			l.version = info.version
		})
	}()
}
