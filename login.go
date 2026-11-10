package main

import (
	_ "embed"
	"strings"

	"github.com/egoist/mygo/ui"

	"immich-mygo/immichapi"
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

// loginStage is the step of the login flow the card shows.
type loginStage uint8

const (
	stageServer loginStage = iota
	stageCredentials
	stageSignedIn
)

// loginState is the login flow: the server address the user types, what
// checking it found, and the credentials they sign in with.
type loginState struct {
	stage loginStage

	serverURL string
	checking  bool
	err       string
	version   string
	// attempt numbers the server checks, so that one that finishes after
	// the address changed is dropped.
	attempt int

	email       string
	password    string
	signingIn   bool
	loginErr    string
	authAttempt int
	user        *immichapi.LoginResponseDto
}

func (l *loginState) view(c *ui.Context, a *app) {
	t := c.Theme()
	ui.Column(c).Fill().Center().Padding(24).Children(func() {
		ui.Column(c).Width(400).Padding(28).Gap(18).Radius(10).
			Background(t.Surface).Border(1, t.Border).Children(func() {
			l.header(c)
			switch l.stage {
			case stageCredentials:
				a.credentials(c)
			case stageSignedIn:
				a.signedIn(c)
			default:
				a.serverForm(c)
			}
		})
	})
}

// header shows the app's mark and name above every step of the flow.
func (l *loginState) header(c *ui.Context) {
	t := c.Theme()
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
}

// serverForm is the first step: the address of the server.
func (a *app) serverForm(c *ui.Context) {
	l := &a.login
	t := c.Theme()
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
		if l.checking {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Label("正在检查服务器")
				ui.Text(c, "正在检查服务器…").FontSize(12).TextColor(t.TextMuted)
			})
		} else if l.version != "" {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(8, 8).Radius(4).Background(t.Success)
				ui.Text(c, "已连接到 Immich "+l.version).FontSize(12).TextColor(t.Success)
			})
		}
	})
}

// credentials is the second step: the account on the checked server.
func (a *app) credentials(c *ui.Context) {
	l := &a.login
	t := c.Theme()
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Text(c, "账号登录").FontSize(18).Bold()
			server := l.serverURL
			if l.version != "" {
				server += " · Immich " + l.version
			}
			ui.Text(c, server).FontSize(12).TextColor(t.TextMuted).SingleLine()
		})
		ui.Button(c, "更换服务器").Disabled(l.signingIn).OnClick(a.backToServer)
	})
	ui.Column(c).Gap(14).Children(func() {
		ui.Field(c, "邮箱", func() {
			ui.TextInput(c, &l.email).
				Placeholder("you@example.com").
				AutoFocus().
				Disabled(l.signingIn).
				OnChange(a.credentialsChanged).
				OnSubmit(a.signIn)
		})
		ui.Field(c, "密码", func() {
			ui.TextInput(c, &l.password).
				Password().
				Disabled(l.signingIn).
				OnChange(a.credentialsChanged).
				OnSubmit(a.signIn)
		})
	})
	if l.loginErr != "" {
		ui.Text(c, l.loginErr).FontSize(12).TextColor(t.Danger)
	}
	ui.Column(c).Gap(10).Children(func() {
		ui.PrimaryButton(c, "登录").Disabled(l.signingIn).OnClick(a.signIn)
		if l.signingIn {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Label("正在登录")
				ui.Text(c, "正在登录…").FontSize(12).TextColor(t.TextMuted)
			})
		}
	})
}

// signedIn is the step after a successful login. The app's screens come
// here next; for now it shows who signed in.
func (a *app) signedIn(c *ui.Context) {
	l := &a.login
	t := c.Theme()
	name, email := "你", ""
	if l.user != nil {
		if l.user.Name != "" {
			name = l.user.Name
		}
		email = string(l.user.UserEmail)
	}
	ui.Column(c).Gap(6).Children(func() {
		ui.Text(c, "欢迎，"+name).FontSize(20).Bold()
		if email != "" {
			ui.Text(c, email).FontSize(12).TextColor(t.TextMuted)
		}
	})
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(8, 8).Radius(4).Background(t.Success)
		status := "已登录"
		if l.version != "" {
			status += " · Immich " + l.version
		}
		ui.Text(c, status).FontSize(12).TextColor(t.Success)
	})
	ui.Button(c, "退出登录").OnClick(a.signOut)
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

// credentialsChanged clears the error of the last attempt once the user
// changes what they sign in with.
func (a *app) credentialsChanged() {
	a.login.loginErr = ""
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
			l.loginErr = ""
			l.password = ""
			l.stage = stageCredentials
		})
	}()
}

// signIn signs in on the checked server with the email and password the
// user typed. Slow work runs off the UI thread and publishes through
// app.update.
func (a *app) signIn() {
	l := &a.login
	if l.signingIn {
		return
	}
	email := strings.TrimSpace(l.email)
	if email == "" {
		l.loginErr = "请输入邮箱"
		return
	}
	if l.password == "" {
		l.loginErr = "请输入密码"
		return
	}
	l.loginErr = ""
	l.signingIn = true
	l.authAttempt++
	authAttempt := l.authAttempt
	base, password := l.serverURL, l.password
	go func() {
		user, err := loginImmich(base, email, password)
		a.update(func() {
			if authAttempt != l.authAttempt {
				// The flow moved on while the request ran.
				return
			}
			l.signingIn = false
			if err != nil {
				l.loginErr = err.Error()
				return
			}
			l.user = user
			l.password = ""
			l.stage = stageSignedIn
		})
	}()
}

// backToServer returns to the server step, ending the attempt to sign in.
func (a *app) backToServer() {
	l := &a.login
	l.authAttempt++
	l.signingIn = false
	l.loginErr = ""
	l.password = ""
	l.stage = stageServer
}

// signOut signs out of the server and returns to the login step. The
// server invalidates the token in the background; the app forgets it now.
func (a *app) signOut() {
	l := &a.login
	l.authAttempt++
	l.signingIn = false
	l.loginErr = ""
	l.password = ""
	if l.user != nil && l.user.AccessToken != "" {
		base, token := l.serverURL, l.user.AccessToken
		go logoutImmich(base, token)
	}
	l.user = nil
	l.stage = stageCredentials
}
