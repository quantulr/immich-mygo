package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

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
