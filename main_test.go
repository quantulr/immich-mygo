package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// The view runs without a window in tests, which click and type as a user
// would.
func TestLoginRejectsUnsupportedAddress(t *testing.T) {
	a := &app{}
	tt := ui.NewTester(a.view, 480, 520)
	if err := tt.Click("服务器地址"); err != nil {
		t.Fatal(err)
	}
	tt.Type("ftp://photos.example.com")
	if err := tt.Click("连接"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("仅支持 http:// 或 https:// 地址") {
		t.Errorf("texts %q after an unsupported address", tt.Texts())
	}
	if a.login.checking {
		t.Error("checking a server after an unsupported address")
	}
}

func TestLoginSubmitsOnEnter(t *testing.T) {
	a := &app{}
	tt := ui.NewTester(a.view, 480, 520)
	// The address input takes the focus as the page opens.
	tt.Type("ftp://photos.example.com")
	tt.Key(0, ui.KeyEnter)
	if !tt.HasText("仅支持 http:// 或 https:// 地址") {
		t.Errorf("texts %q after Enter", tt.Texts())
	}
}

func TestLoginRequiresAddress(t *testing.T) {
	a := &app{}
	tt := ui.NewTester(a.view, 480, 520)
	if err := tt.Click("连接"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("请输入服务器地址") {
		t.Errorf("texts %q after an empty address", tt.Texts())
	}
}

func TestLoginRequiresCredentials(t *testing.T) {
	a := &app{}
	a.login.stage = stageCredentials
	a.login.serverURL = "https://photos.example.com"
	tt := ui.NewTester(a.view, 480, 560)
	if err := tt.Click("登录"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("请输入邮箱") {
		t.Errorf("texts %q after an empty email", tt.Texts())
	}
	if err := tt.Click("邮箱"); err != nil {
		t.Fatal(err)
	}
	tt.Type("ada@example.com")
	if err := tt.Click("登录"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("请输入密码") {
		t.Errorf("texts %q after an empty password", tt.Texts())
	}
	if a.login.signingIn {
		t.Error("signing in without a password")
	}
}

func TestBackToServer(t *testing.T) {
	a := &app{}
	a.login.stage = stageCredentials
	a.login.serverURL = "https://photos.example.com"
	tt := ui.NewTester(a.view, 480, 560)
	if err := tt.Click("更换服务器"); err != nil {
		t.Fatal(err)
	}
	if a.login.stage != stageServer {
		t.Errorf("stage %d after going back, want server", a.login.stage)
	}
	if !tt.HasText("服务器地址") {
		t.Errorf("texts %q after going back", tt.Texts())
	}
}

func TestCancelRestore(t *testing.T) {
	a := &app{}
	a.login.stage = stageRestoring
	a.login.serverURL = "https://photos.example.com"
	tt := ui.NewTester(a.view, 480, 520)
	if !tt.HasText("正在验证登录状态") {
		t.Errorf("texts %q while restoring", tt.Texts())
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if a.login.stage != stageCredentials {
		t.Errorf("stage %d after cancelling, want credentials", a.login.stage)
	}
	if !tt.HasText("账号登录") {
		t.Errorf("texts %q after cancelling", tt.Texts())
	}
}

func TestSignOutReturnsToCredentials(t *testing.T) {
	withTestSession(t)
	a := &app{}
	a.login.stage = stageSignedIn
	a.login.serverURL = "https://photos.example.com"
	a.login.version = "2.1.0"
	a.login.user = &account{Name: "Ada", Email: "ada@example.com"}
	tt := ui.NewTester(a.view, 1120, 720)
	if !tt.HasText("照片") {
		t.Errorf("texts %q while signed in", tt.Texts())
	}
	if err := tt.Click("退出登录"); err != nil {
		t.Fatal(err)
	}
	if a.login.stage != stageCredentials || a.login.user != nil {
		t.Error("still signed in after signing out")
	}
	if !tt.HasText("账号登录") {
		t.Errorf("texts %q after signing out", tt.Texts())
	}
}

func TestNormalizeServerURL(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"photos.example.com", "https://photos.example.com", false},
		{" https://photos.example.com/ ", "https://photos.example.com", false},
		{"https://photos.example.com/api", "https://photos.example.com", false},
		{"https://photos.example.com/api/", "https://photos.example.com", false},
		{"https://photos.example.com/immich/api", "https://photos.example.com/immich", false},
		{"http://192.168.1.20:2283", "http://192.168.1.20:2283", false},
		{"", "", true},
		{"ftp://photos.example.com", "", true},
		{"https://", "", true},
		{"https://user:pass@photos.example.com", "", true},
		{"https://photos.example.com/?a=1", "", true},
		{"https://photos.example.com/#top", "", true},
	}
	for _, c := range cases {
		got, err := normalizeServerURL(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("normalizeServerURL(%q) = %q, want an error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizeServerURL(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("normalizeServerURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPingImmich(t *testing.T) {
	// Current Immich serves these under /server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/server/ping":
			io.WriteString(w, `{"res":"pong"}`)
		case "/api/server/version":
			io.WriteString(w, `{"major":2,"minor":1,"patch":0,"featureLevel":1}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	info, err := pingImmich(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if info.version != "2.1.0" {
		t.Errorf("version %q, want 2.1.0", info.version)
	}
}

func TestPingImmichLegacyServer(t *testing.T) {
	// Servers from before Immich 2.x serve these under /server-info.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/server-info/ping":
			io.WriteString(w, `{"res":"pong"}`)
		case "/api/server-info/version":
			io.WriteString(w, `{"major":1,"minor":119,"patch":1}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	info, err := pingImmich(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if info.version != "1.119.1" {
		t.Errorf("version %q, want 1.119.1", info.version)
	}
}

func TestPingImmichRejectsOtherServers(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"not found": http.NotFound,
		"not pong": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"res":"hello"}`)
		},
		"not json": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `<html></html>`)
		},
		"server error": func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		},
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(handler)
			defer srv.Close()
			if _, err := pingImmich(srv.URL); err == nil {
				t.Fatal("pingImmich accepted a server that is not Immich")
			}
		})
	}
}

func TestLoginImmich(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/auth/login" {
			http.NotFound(w, r)
			return
		}
		var cred struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&cred); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case cred.Email == "ada@example.com" && cred.Password == "secret":
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{
				"accessToken":"token-123",
				"userId":"3fa85f64-5717-4562-b3fc-2c963f66afa6",
				"userEmail":"ada@example.com",
				"name":"Ada",
				"profileImagePath":"",
				"isAdmin":false,
				"shouldChangePassword":false,
				"isOnboarded":true
			}`)
		case cred.Email == "disabled@example.com":
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"message":"Password login has been disabled","error":"Unauthorized","statusCode":401}`)
		default:
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"message":"Incorrect email or password","error":"Unauthorized","statusCode":401}`)
		}
	}))
	defer srv.Close()

	user, err := loginImmich(srv.URL, "ada@example.com", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if user.Token != "token-123" || user.Name != "Ada" ||
		user.Email != "ada@example.com" {
		t.Errorf("signed in as %+v", user)
	}

	if _, err := loginImmich(srv.URL, "ada@example.com", "wrong"); err == nil ||
		err.Error() != "邮箱或密码不正确" {
		t.Errorf("wrong password: %v", err)
	}
	if _, err := loginImmich(srv.URL, "disabled@example.com", "secret"); err == nil ||
		err.Error() != "该服务器已禁用密码登录" {
		t.Errorf("disabled password login: %v", err)
	}
}

func TestFetchAccount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users/me" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer token-123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{
			"id":"3fa85f64-5717-4562-b3fc-2c963f66afa6",
			"email":"ada@example.com",
			"name":"Ada",
			"isAdmin":true
		}`)
	}))
	defer srv.Close()

	user, err := fetchAccount(srv.URL, "token-123")
	if err != nil {
		t.Fatal(err)
	}
	if user.ID != "3fa85f64-5717-4562-b3fc-2c963f66afa6" || user.Name != "Ada" ||
		user.Email != "ada@example.com" || !user.Admin || user.Token != "token-123" {
		t.Errorf("account %+v", user)
	}

	if _, err := fetchAccount(srv.URL, "wrong"); !errors.Is(err, errSessionExpired) {
		t.Errorf("refused token: %v", err)
	}
}

func TestFramedHidesTitleBar(t *testing.T) {
	old := ownControls
	defer func() { ownControls = old }()

	ownControls = true
	opts := framed(mygo.WindowOptions{})
	if opts.TitleBarStyle != mygo.TitleBarHidden || !opts.HideWindowButtons ||
		!opts.AutoHideMenuBar {
		t.Errorf("framed on Windows or Linux: %+v", opts)
	}

	ownControls = false
	opts = framed(mygo.WindowOptions{})
	if opts.TitleBarStyle != mygo.TitleBarHiddenInset || opts.TitleBarHeight != topBar ||
		opts.TrafficLightPosition == nil {
		t.Errorf("framed on macOS: %+v", opts)
	}
}

// waitFor waits until ok holds, as a load off the UI thread finishes.
func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// testPNG is a thumbnail for a fake server.
func testPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const testAssetID = "3fa85f64-5717-4562-b3fc-2c963f66afa6"

// timelineServer serves a timeline of two assets with their thumbnails,
// and one album.
func timelineServer(t *testing.T) *httptest.Server {
	t.Helper()
	thumb := testPNG(t)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/timeline/buckets":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `[{"timeBucket":"2026-11-01","count":2}]`)
		case r.URL.Path == "/api/timeline/bucket":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{
				"id":["`+testAssetID+`","00000000-0000-0000-0000-000000000002"],
				"isImage":[true,false],
				"ratio":[1.5,0.75]
			}`)
		case r.URL.Path == "/api/assets/"+testAssetID+"/thumbnail",
			r.URL.Path == "/api/assets/00000000-0000-0000-0000-000000000002/thumbnail":
			w.Header().Set("Content-Type", "image/png")
			w.Write(thumb)
		case r.URL.Path == "/api/albums":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `[
				{"albumName":"旅行","assetCount":3,"id":"`+testAssetID+`","albumThumbnailAssetId":"`+testAssetID+`"}
			]`)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestMainTimeline(t *testing.T) {
	srv := timelineServer(t)
	defer srv.Close()
	withTestSession(t)

	a := &app{}
	a.login.stage = stageSignedIn
	a.login.serverURL = srv.URL
	a.login.user = &account{Name: "Ada", Email: "ada@example.com", Token: "token-123"}
	a.enterMain()

	waitFor(t, "the timeline", func() bool {
		f := &a.main.photos
		return f.loaded && len(f.assets) == 2 &&
			f.assets[0].Bm != nil && f.assets[1].Bm != nil
	})

	tt := ui.NewTester(a.view, 1120, 720)
	if !tt.HasText("照片") || !tt.HasText("Ada") {
		t.Errorf("texts %q in the main view", tt.Texts())
	}
}

func TestMainSections(t *testing.T) {
	srv := timelineServer(t)
	defer srv.Close()
	withTestSession(t)

	a := &app{}
	a.login.stage = stageSignedIn
	a.login.serverURL = srv.URL
	a.login.user = &account{Name: "Ada", Email: "ada@example.com", Token: "token-123"}
	a.enterMain()

	tt := ui.NewTester(a.view, 1120, 720)
	if err := tt.Click("相册"); err != nil {
		t.Fatal(err)
	}
	if a.main.section != sectionAlbums {
		t.Fatalf("section %d after choosing albums", a.main.section)
	}
	waitFor(t, "the albums", func() bool {
		f := &a.main.albums
		return f.loaded && len(f.albums) == 1 && f.albums[0].Bm != nil
	})
	tt.Frame()
	if !tt.HasText("旅行") {
		t.Errorf("texts %q in the albums section", tt.Texts())
	}
}

func TestLoginFlowReachesMain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/server/ping":
			io.WriteString(w, `{"res":"pong"}`)
		case "/api/server/version":
			io.WriteString(w, `{"major":2,"minor":1,"patch":0}`)
		case "/api/auth/login":
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{
				"accessToken":"token-123",
				"userId":"`+testAssetID+`",
				"userEmail":"ada@example.com",
				"name":"Ada"
			}`)
		case "/api/timeline/buckets":
			io.WriteString(w, `[]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	secrets, _ := withTestSession(t)

	a := &app{}
	tt := ui.NewTester(a.view, 480, 520)
	tt.Type(srv.URL)
	tt.Key(0, ui.KeyEnter)
	waitFor(t, "the server check", func() bool {
		return a.login.stage == stageCredentials && !a.login.checking
	})

	tt.Frame()
	if err := tt.Click("邮箱"); err != nil {
		t.Fatal(err)
	}
	tt.Type("ada@example.com")
	if err := tt.Click("密码"); err != nil {
		t.Fatal(err)
	}
	tt.Type("secret")
	if err := tt.Click("登录"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the main view", func() bool {
		return a.login.stage == stageSignedIn && a.main.photos.loaded
	})

	tt.Frame()
	if !tt.HasText("照片") || !tt.HasText("Ada") {
		t.Errorf("texts %q after a login", tt.Texts())
	}
	if string(secrets[accessTokenKey]) != "token-123" {
		t.Errorf("saved token %q after a login", secrets[accessTokenKey])
	}
}
