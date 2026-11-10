package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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

func TestSidebarToggles(t *testing.T) {
	a := &app{}
	a.login.stage = stageSignedIn
	a.login.serverURL = "https://photos.example.com"
	a.login.user = &account{Name: "Ada", Email: "ada@example.com"}
	tt := ui.NewTester(a.view, 1200, 720)
	tt.Advance(500 * time.Millisecond)
	tt.Frame()

	if err := tt.Click("隐藏边栏"); err != nil {
		t.Fatal(err)
	}
	if !a.main.sidebarHidden {
		t.Fatal("the sidebar is not hidden")
	}
	tt.Advance(500 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("显示边栏") {
		t.Errorf("texts %q with the sidebar hidden", tt.Texts())
	}

	if err := tt.Click("显示边栏"); err != nil {
		t.Fatal(err)
	}
	if a.main.sidebarHidden {
		t.Error("the sidebar is still hidden")
	}
	tt.Advance(500 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("隐藏边栏") {
		t.Errorf("texts %q with the sidebar back", tt.Texts())
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

const (
	testAssetID      = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	testOtherAssetID = "00000000-0000-0000-0000-000000000002"
	testVideoID      = "00000000-0000-0000-0000-000000000003"
)

// timelineServer serves two days of the timeline with their thumbnails, one
// holding an image and a video, and one album.
func timelineServer(t *testing.T) *httptest.Server {
	t.Helper()
	thumb := testPNG(t)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/timeline/buckets":
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Query().Get("albumId") == testAssetID {
				io.WriteString(w, `[{"timeBucket":"2024-04-01","count":2}]`)
				return
			}
			io.WriteString(w, `[
				{"timeBucket":"2024-05-02","count":1},
				{"timeBucket":"2024-05-01","count":2}
			]`)
		case r.URL.Path == "/api/timeline/bucket":
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Query().Get("albumId") == testAssetID {
				io.WriteString(w, `{
					"id":["`+testOtherAssetID+`","`+testVideoID+`"],
					"isImage":[true,false],
					"ratio":[1.2,0.75],
					"duration":[null,42000]
				}`)
				return
			}
			switch r.URL.Query().Get("timeBucket") {
			case "2024-05-02":
				io.WriteString(w, `{
					"id":["`+testAssetID+`"],
					"isImage":[true],
					"ratio":[1.5],
					"duration":[null]
				}`)
			case "2024-05-01":
				io.WriteString(w, `{
					"id":["`+testOtherAssetID+`","`+testVideoID+`"],
					"isImage":[true,false],
					"ratio":[1.2,0.75],
					"duration":[null,42000]
				}`)
			default:
				http.NotFound(w, r)
			}
		case strings.HasPrefix(r.URL.Path, "/api/assets/") &&
			strings.HasSuffix(r.URL.Path, "/thumbnail"):
			w.Header().Set("Content-Type", "image/png")
			w.Write(thumb)
		case strings.HasPrefix(r.URL.Path, "/api/assets/"):
			id := strings.TrimPrefix(r.URL.Path, "/api/assets/")
			name, kind, duration := "IMG_0001.jpg", "IMAGE", ""
			width, height := 4032, 3024
			switch id {
			case testOtherAssetID:
				name = "IMG_0002.jpg"
			case testVideoID:
				name, kind, duration = "VID_0003.mp4", "VIDEO", `"duration":42000,`
				width, height = 1920, 1080
			case testAssetID:
			default:
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{
				"originalFileName":"`+name+`",
				"localDateTime":"2024-05-02T14:32:00.000Z",
				`+duration+`
				"width":`+strconv.Itoa(width)+`,
				"height":`+strconv.Itoa(height)+`,
				"type":"`+kind+`",
				"exifInfo":{
					"make":"Apple","model":"iPhone 15 Pro",
					"lensModel":"iPhone 15 Pro back camera 6.765mm f/1.78",
					"fNumber":1.78,"exposureTime":"1/120","iso":100,"focalLength":6.765,
					"fileSizeInByte":4194304,
					"city":"杭州","state":"浙江","country":"中国",
					"description":"午后的西湖",
					"latitude":30.2741,"longitude":120.1551,
					"timeZone":"Asia/Shanghai",
					"exifImageWidth":`+strconv.Itoa(width)+`,
					"exifImageHeight":`+strconv.Itoa(height)+`
				}
			}`)
		case r.URL.Path == "/api/albums":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `[
				{"albumName":"旅行","assetCount":2,"id":"`+testAssetID+`","albumThumbnailAssetId":"`+testAssetID+`"}
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

	waitFor(t, "the first day", func() bool {
		f := &a.main.photos
		return f.loaded && len(f.days) == 2 && f.days[0].loaded &&
			len(f.days[0].photos) == 1 && f.days[0].photos[0].Bm != nil
	})

	tt := ui.NewTester(a.view, 1120, 720)
	// The list tells what it shows after a layout; the frame after loads
	// the day below it.
	tt.Frame()
	tt.Frame()
	waitFor(t, "the second day", func() bool {
		d := a.main.photos.days[1]
		return d.loaded && len(d.photos) == 2 &&
			d.photos[0].Bm != nil && d.photos[1].Bm != nil
	})
	tt.Frame()

	titles := []string{"5月2日", "5月1日", "周四", "周三"}
	for _, want := range titles {
		if !tt.HasText(want) {
			t.Errorf("texts %q do not show %q", tt.Texts(), want)
		}
	}
	video := a.main.photos.days[1].photos[1]
	if !video.Video || video.Duration != 42000 {
		t.Errorf("video %+v", video)
	}
	if !tt.HasText("0:42") {
		t.Errorf("texts %q do not show the video's length", tt.Texts())
	}
}

// TestPhotoPage opens a photo from the timeline, walks the timeline with
// the buttons and the keyboard, and returns to it with Escape.
func TestPhotoPage(t *testing.T) {
	srv := timelineServer(t)
	defer srv.Close()
	withTestSession(t)

	a := &app{}
	a.login.stage = stageSignedIn
	a.login.serverURL = srv.URL
	a.login.user = &account{Name: "Ada", Email: "ada@example.com", Token: "token-123"}
	a.enterMain()

	tt := ui.NewTester(a.view, 1120, 720)
	tt.Frame()
	tt.Frame()
	waitFor(t, "the days", func() bool {
		f := &a.main.photos
		return f.loaded && len(f.days) == 2 && f.days[0].loaded &&
			len(f.days[0].photos) == 1 && f.days[1].loaded &&
			len(f.days[1].photos) == 2
	})
	tt.Frame()

	if err := tt.Click("照片 1"); err != nil {
		t.Fatal(err)
	}
	if a.main.viewer == nil {
		t.Fatal("the image page did not open")
	}
	waitFor(t, "the image", func() bool { return a.main.viewer.bm != nil })
	tt.Frame()
	for _, want := range []string{"返回时间线", "5月2日", "1 / 3"} {
		if !tt.HasText(want) {
			t.Errorf("texts %q on the image page do not show %q", tt.Texts(), want)
		}
	}

	// The next photo is the first of the older day.
	if err := tt.Click("下一张"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the next image", func() bool {
		return a.main.viewer != nil && a.main.viewer.id == testOtherAssetID &&
			a.main.viewer.bm != nil
	})
	tt.Frame()
	if !tt.HasText("5月1日") || !tt.HasText("2 / 3") {
		t.Errorf("texts %q after the next photo", tt.Texts())
	}

	// The keyboard walks too: right to the video, shown as its still.
	tt.Key(0, ui.KeyRight)
	waitFor(t, "the video's still", func() bool {
		return a.main.viewer != nil && a.main.viewer.id == testVideoID &&
			a.main.viewer.bm != nil
	})
	tt.Frame()
	if !tt.HasText("视频") || !tt.HasText("0:42") || !tt.HasText("3 / 3") {
		t.Errorf("texts %q on the video", tt.Texts())
	}
	// The last photo has no next: its button is disabled, and the page
	// stays.
	tt.Click("下一张")
	if a.main.viewer == nil || a.main.viewer.id != testVideoID {
		t.Error("the page moved past the last photo")
	}
	tt.Frame()
	if !tt.HasText("3 / 3") {
		t.Errorf("texts %q after the last photo's next", tt.Texts())
	}

	// Escape returns to the timeline.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.main.viewer != nil {
		t.Error("the image page is still open after Escape")
	}
	if !tt.HasText("照片 1") {
		t.Errorf("texts %q back in the timeline", tt.Texts())
	}
}

// TestPhotoPageZoom zooms the photo with the buttons, the wheel, a double
// click and the keyboard, pans it with a drag, and shows another photo at
// fit again.
func TestPhotoPageZoom(t *testing.T) {
	srv := timelineServer(t)
	defer srv.Close()
	withTestSession(t)

	a := &app{}
	a.login.stage = stageSignedIn
	a.login.serverURL = srv.URL
	a.login.user = &account{Name: "Ada", Email: "ada@example.com", Token: "token-123"}
	a.enterMain()

	tt := ui.NewTester(a.view, 1120, 720)
	tt.Frame()
	tt.Frame()
	waitFor(t, "the days", func() bool {
		f := &a.main.photos
		return f.loaded && len(f.days) == 2 && f.days[0].loaded &&
			len(f.days[0].photos) == 1 && f.days[1].loaded &&
			len(f.days[1].photos) == 2
	})
	tt.Frame()
	if err := tt.Click("照片 1"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the image", func() bool {
		return a.main.viewer != nil && a.main.viewer.bm != nil
	})
	tt.Frame()
	tt.Frame()

	if z := a.main.viewer.zoomFactor(); z != 1 {
		t.Errorf("zoom %v on opening, want 1", z)
	}
	if !tt.HasText("100%") {
		t.Errorf("texts %q at fit", tt.Texts())
	}

	// The buttons step the zoom, and the percentage tells it.
	if err := tt.Click("放大"); err != nil {
		t.Fatal(err)
	}
	if z := a.main.viewer.zoomFactor(); z != zoomStep {
		t.Errorf("zoom %v after zooming in, want %v", z, zoomStep)
	}
	tt.Frame()
	if !tt.HasText("125%") {
		t.Errorf("texts %q after zooming in", tt.Texts())
	}
	if err := tt.Click("缩小"); err != nil {
		t.Fatal(err)
	}
	if z := a.main.viewer.zoomFactor(); z != 1 {
		t.Errorf("zoom %v after zooming out, want 1", z)
	}

	// The wheel zooms about the pointer, and the percentage goes back to
	// fit.
	tt.Wheel(600, 400, 0, -120)
	if z := a.main.viewer.zoomFactor(); z <= 1 {
		t.Errorf("zoom %v after the wheel, want more than 1", z)
	}
	tt.Frame()
	if err := tt.Click("适应窗口"); err != nil {
		t.Fatal(err)
	}
	if z, x, y := a.main.viewer.zoomFactor(), a.main.viewer.panX, a.main.viewer.panY; z != 1 || x != 0 || y != 0 {
		t.Errorf("zoom %v, pan (%v, %v) after going back to fit", z, x, y)
	}
	tt.Frame()
	if !tt.HasText("100%") {
		t.Errorf("texts %q back at fit", tt.Texts())
	}

	// A double click zooms to twice the fit, and again shows it at fit.
	tt.Advance(600 * time.Millisecond)
	tt.ClickAt(600, 400)
	tt.ClickAt(600, 400)
	tt.Frame()
	if z := a.main.viewer.zoomFactor(); z != 2 {
		t.Errorf("zoom %v after a double click, want 2", z)
	}
	tt.Advance(600 * time.Millisecond)
	tt.ClickAt(600, 400)
	tt.ClickAt(600, 400)
	tt.Frame()
	if z := a.main.viewer.zoomFactor(); z != 1 {
		t.Errorf("zoom %v after another double click, want 1", z)
	}

	// A drag pans the zoomed photo.
	tt.Advance(600 * time.Millisecond)
	tt.ClickAt(600, 400)
	tt.ClickAt(600, 400)
	tt.Frame()
	beforeX, beforeY := a.main.viewer.panX, a.main.viewer.panY
	tt.Advance(600 * time.Millisecond)
	tt.Press(600, 400)
	tt.Move(540, 360)
	tt.Frame()
	tt.Release(540, 360)
	tt.Frame()
	if a.main.viewer.panX >= beforeX || a.main.viewer.panY >= beforeY {
		t.Errorf("pan (%v, %v) after a drag, was (%v, %v)",
			a.main.viewer.panX, a.main.viewer.panY, beforeX, beforeY)
	}

	// The keyboard zooms about the center, and 0 goes back to fit.
	tt.Key(0, ui.Key0)
	tt.Frame()
	if z := a.main.viewer.zoomFactor(); z != 1 {
		t.Errorf("zoom %v after 0, want 1", z)
	}
	tt.Key(0, ui.KeyEqual)
	if z := a.main.viewer.zoomFactor(); z != zoomStep {
		t.Errorf("zoom %v after +, want %v", z, zoomStep)
	}
	tt.Frame()
	if !tt.HasText("125%") {
		t.Errorf("texts %q after +", tt.Texts())
	}

	// Another photo shows at fit again.
	if err := tt.Click("下一张"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the next image", func() bool {
		return a.main.viewer != nil && a.main.viewer.bm != nil &&
			a.main.viewer.id == testOtherAssetID
	})
	tt.Frame()
	if z := a.main.viewer.zoomFactor(); z != 1 {
		t.Errorf("zoom %v on the next photo, want 1", z)
	}
	if !tt.HasText("100%") {
		t.Errorf("texts %q on the next photo", tt.Texts())
	}
}

// TestPhotoPageDetails opens the image page's details panel, shows what it
// tells, keeps the wheel off the photo where the panel stands, follows the
// page to other photos, and closes it from the panel and the header.
func TestPhotoPageDetails(t *testing.T) {
	srv := timelineServer(t)
	defer srv.Close()
	withTestSession(t)

	a := &app{}
	a.login.stage = stageSignedIn
	a.login.serverURL = srv.URL
	a.login.user = &account{Name: "Ada", Email: "ada@example.com", Token: "token-123"}
	a.enterMain()

	tt := ui.NewTester(a.view, 1120, 720)
	tt.Frame()
	tt.Frame()
	waitFor(t, "the days", func() bool {
		f := &a.main.photos
		return f.loaded && len(f.days) == 2 && f.days[0].loaded &&
			len(f.days[0].photos) == 1 && f.days[1].loaded &&
			len(f.days[1].photos) == 2
	})
	tt.Frame()
	if err := tt.Click("照片 1"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the image", func() bool {
		return a.main.viewer != nil && a.main.viewer.bm != nil
	})
	tt.Frame()

	// The header button opens the panel, which loads what the asset says.
	if err := tt.Click("详情"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the details", func() bool {
		v := a.main.viewer
		return v != nil && v.showDetails && v.details != nil
	})
	tt.Frame()
	for _, want := range []string{
		"隐藏详情", "关闭详情", "文件名", "IMG_0001.jpg",
		"拍摄时间", "2024年5月2日 14:32 · Asia/Shanghai",
		"尺寸", "4032 × 3024 · 12.2 MP", "大小", "4.0 MB",
		"相机", "Apple iPhone 15 Pro",
		"镜头", "iPhone 15 Pro back camera 6.765mm f/1.78",
		"曝光", "f/1.78 · 1/120 · ISO 100 · 6.765 mm",
		"位置", "杭州 · 浙江 · 中国", "描述", "午后的西湖",
	} {
		if !tt.HasText(want) {
			t.Errorf("texts %q do not show %q", tt.Texts(), want)
		}
	}

	// The wheel over the panel leaves the photo alone; over the photo it
	// zooms.
	if z := a.main.viewer.zoomFactor(); z != 1 {
		t.Errorf("zoom %v on opening the details, want 1", z)
	}
	tt.Wheel(1050, 300, 0, -120)
	if z := a.main.viewer.zoomFactor(); z != 1 {
		t.Errorf("zoom %v after the wheel over the details, want 1", z)
	}
	tt.Wheel(600, 400, 0, -120)
	if z := a.main.viewer.zoomFactor(); z <= 1 {
		t.Errorf("zoom %v after the wheel over the photo, want more than 1", z)
	}
	tt.Key(0, ui.Key0)
	tt.Frame()

	// The next photo brings its own details with it.
	if err := tt.Click("下一张"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the next image's details", func() bool {
		v := a.main.viewer
		return v != nil && v.id == testOtherAssetID && v.bm != nil &&
			v.details != nil && v.details.Name == "IMG_0002.jpg"
	})
	tt.Frame()
	if !tt.HasText("IMG_0002.jpg") || tt.HasText("IMG_0001.jpg") {
		t.Errorf("texts %q on the next photo's details", tt.Texts())
	}

	// A video's details tell how long it runs.
	if err := tt.Click("下一张"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the video's details", func() bool {
		v := a.main.viewer
		return v != nil && v.id == testVideoID && v.details != nil &&
			v.details.Name == "VID_0003.mp4"
	})
	tt.Frame()
	if !tt.HasText("VID_0003.mp4") || !tt.HasText("时长") || !tt.HasText("0:42") {
		t.Errorf("texts %q on the video's details", tt.Texts())
	}

	// The panel's close button hides it; the header button brings it back
	// without loading it again.
	if err := tt.Click("关闭详情"); err != nil {
		t.Fatal(err)
	}
	// A click that changes the page draws it on the frame after the one
	// that took it.
	tt.Frame()
	tt.Frame()
	if v := a.main.viewer; v == nil || v.showDetails {
		t.Error("the details panel is still open")
	}
	if tt.HasText("VID_0003.mp4") {
		t.Errorf("texts %q after closing the details", tt.Texts())
	}
	if err := tt.Click("详情"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if !tt.HasText("隐藏详情") || !tt.HasText("VID_0003.mp4") {
		t.Errorf("texts %q after opening the details again", tt.Texts())
	}
	if a.main.viewer == nil || !a.main.viewer.showDetails {
		t.Error("the details panel did not open again")
	}

	// While the image loads, the page says so in the photo's room, clear of
	// the panel.
	bm := a.main.viewer.bm
	a.main.viewer.bm = nil
	tt.Frame()
	load, ok := tt.Find("正在加载图片…")
	if !ok {
		t.Fatal("the loading text does not show")
	}
	prev, okPrev := tt.Find("上一张")
	title, okTitle := tt.Find("详情")
	if okPrev && okTitle {
		// The left arrow sits 16 DIPs into the stage; the panel's title 16
		// DIPs into the panel.
		stageLeft, panelLeft := prev.X-16, title.X-16
		roomCenter := (stageLeft + panelLeft) / 2
		if center := load.X + load.W/2; center < roomCenter-6 || center > roomCenter+6 {
			t.Errorf("the loading text centers at %v, the photo's room at %v",
				center, roomCenter)
		}
	}
	a.main.viewer.bm = bm
	tt.Frame()
}

// TestPhotoPageError keeps the page open on a photo whose image the server
// will not give, offering to try again.
func TestPhotoPageError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/timeline/buckets":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `[{"timeBucket":"2024-05-02","count":1}]`)
		case "/api/timeline/bucket":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{
				"id":["`+testAssetID+`"],
				"isImage":[true],
				"ratio":[1.5],
				"duration":[null]
			}`)
		default:
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	withTestSession(t)

	a := &app{}
	a.login.stage = stageSignedIn
	a.login.serverURL = srv.URL
	a.login.user = &account{Name: "Ada", Email: "ada@example.com", Token: "token-123"}
	a.enterMain()

	tt := ui.NewTester(a.view, 1120, 720)
	tt.Frame()
	tt.Frame()
	waitFor(t, "the day", func() bool {
		f := &a.main.photos
		return f.loaded && len(f.days) == 1 && f.days[0].loaded &&
			len(f.days[0].photos) == 1
	})
	tt.Frame()

	if err := tt.Click("照片 1"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the failed image", func() bool {
		return a.main.viewer != nil && a.main.viewer.err != nil
	})
	tt.Frame()
	if a.main.viewer.bm != nil {
		t.Error("a bitmap after the image failed")
	}
	if !tt.HasText("无法显示这张图片") || !tt.HasText("重试") {
		t.Errorf("texts %q on the failed image", tt.Texts())
	}

	// Trying again keeps the page, and the error.
	if err := tt.Click("重试"); err != nil {
		t.Fatal(err)
	}
	if a.main.viewer == nil {
		t.Fatal("the page closed on a retry")
	}
	waitFor(t, "the retry to fail", func() bool {
		return a.main.viewer != nil && a.main.viewer.err != nil
	})
	tt.Frame()
	if !tt.HasText("无法显示这张图片") {
		t.Errorf("texts %q after a retry", tt.Texts())
	}
}

// TestTimelineLoadsScrolledDays fills more than the card with one day: the
// day below it loads as the timeline scrolls to it, not before.
func TestTimelineLoadsScrolledDays(t *testing.T) {
	const n = 120
	ids := make([]string, n)
	isImage := make([]bool, n)
	ratio := make([]float64, n)
	durations := make([]*int, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("00000000-0000-0000-0000-%012d", i+10)
		isImage[i] = true
		ratio[i] = 1
	}
	first, err := json.Marshal(map[string]any{
		"id": ids, "isImage": isImage, "ratio": ratio, "duration": durations,
	})
	if err != nil {
		t.Fatal(err)
	}
	thumb := testPNG(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/timeline/buckets":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `[
				{"timeBucket":"2024-05-02","count":120},
				{"timeBucket":"2024-05-01","count":1}
			]`)
		case r.URL.Path == "/api/timeline/bucket":
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Query().Get("timeBucket") == "2024-05-01" {
				io.WriteString(w, `{
					"id":["`+testAssetID+`"],
					"isImage":[true],
					"ratio":[1],
					"duration":[null]
				}`)
				return
			}
			w.Write(first)
		case strings.HasSuffix(r.URL.Path, "/thumbnail"):
			w.Header().Set("Content-Type", "image/png")
			w.Write(thumb)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	withTestSession(t)

	a := &app{}
	a.login.stage = stageSignedIn
	a.login.serverURL = srv.URL
	a.login.user = &account{Name: "Ada", Email: "ada@example.com", Token: "token-123"}
	a.enterMain()

	waitFor(t, "the first day", func() bool {
		f := &a.main.photos
		return f.loaded && len(f.days) == 2 && f.days[0].loaded &&
			len(f.days[0].photos) == n
	})
	tt := ui.NewTester(a.view, 1120, 720)
	tt.Frame()
	tt.Frame()
	if d := a.main.photos.days[1]; d.loaded || d.loading {
		t.Fatal("the day below the view loaded before it showed")
	}

	tt.Scroll(600, 400, 0, 20000)
	tt.Frame()
	tt.Frame()
	waitFor(t, "the day scrolled to", func() bool {
		d := a.main.photos.days[1]
		return d.loaded && len(d.photos) == 1
	})
}

func TestBucketTitle(t *testing.T) {
	now := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		bucket string
		title  string
		week   string
	}{
		{"2026-11-01", "11月1日", "周日"},
		{"2024-05-01", "2024年5月1日", "周三"},
		{"2026-11", "2026年11月", ""},
		{"2026", "2026年", ""},
		{"not a bucket", "not a bucket", ""},
	}
	for _, c := range cases {
		title, week := bucketTitle(c.bucket, now)
		if title != c.title || week != c.week {
			t.Errorf("bucketTitle(%q) = %q, %q; want %q, %q",
				c.bucket, title, week, c.title, c.week)
		}
	}
}

func TestDurationLabel(t *testing.T) {
	cases := map[int]string{
		0:       "0:00",
		42000:   "0:42",
		3599000: "59:59",
		3600000: "1:00:00",
		3661000: "1:01:01",
	}
	for ms, want := range cases {
		if got := durationLabel(ms); got != want {
			t.Errorf("durationLabel(%d) = %q, want %q", ms, got, want)
		}
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

func TestAlbumPage(t *testing.T) {
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
	waitFor(t, "the albums", func() bool {
		f := &a.main.albums
		return f.loaded && len(f.albums) == 1 && f.albums[0].Bm != nil
	})
	tt.Frame()
	if err := tt.Click("旅行"); err != nil {
		t.Fatal(err)
	}
	if a.main.albumOpen == nil {
		t.Fatal("the album did not open")
	}
	waitFor(t, "the album's day", func() bool {
		f := &a.main.albumFeed
		return f.loaded && len(f.days) == 1 && f.days[0].loaded &&
			len(f.days[0].photos) == 2 && f.days[0].photos[0].Bm != nil
	})
	tt.Frame()
	for _, want := range []string{"旅行", "返回相册", "4月1日", "2 项"} {
		if !tt.HasText(want) {
			t.Errorf("texts %q in the album do not show %q", tt.Texts(), want)
		}
	}

	// A photo of the album opens the image page, and the way back returns
	// to the album's days, not to the grid.
	if err := tt.Click("照片 1"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the album's image", func() bool {
		v := a.main.viewer
		return v != nil && v.id == testOtherAssetID && v.bm != nil
	})
	tt.Frame()
	if !tt.HasText("4月1日") || !tt.HasText("1 / 2") {
		t.Errorf("texts %q on the album's image page", tt.Texts())
	}
	if err := tt.Click("返回时间线"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.main.viewer != nil || a.main.albumOpen == nil {
		t.Error("the image page did not return to the album")
	}
	if !tt.HasText("返回相册") || !tt.HasText("4月1日") {
		t.Errorf("texts %q back in the album", tt.Texts())
	}

	if err := tt.Click("返回相册"); err != nil {
		t.Fatal(err)
	}
	if a.main.albumOpen != nil {
		t.Error("still in the album after going back")
	}
	tt.Frame()
	if !tt.HasText("旅行") || !tt.HasText("2 项") {
		t.Errorf("texts %q back in the grid", tt.Texts())
	}

	// The same album opens again, its days kept.
	if err := tt.Click("旅行"); err != nil {
		t.Fatal(err)
	}
	if a.main.albumOpen == nil {
		t.Fatal("the album did not open again")
	}
	tt.Frame()
	if !tt.HasText("4月1日") {
		t.Errorf("texts %q after opening the album again", tt.Texts())
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
