package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestVideoServerStreams checks that the loopback server gives the system's
// player the stream of an asset: it adds the session's token to what it asks
// of Immich, passes the player's ranges on, and answers with what Immich
// says of the file.
func TestVideoServerStreams(t *testing.T) {
	const sample = "0123456789"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/assets/"+testVideoID+"/video/playback" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token-123" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Accept-Ranges", "bytes")
		if rng := r.Header.Get("Range"); rng != "" {
			// The server passes on exactly the range the player asked for.
			var from, to int
			if _, err := fmt.Sscanf(rng, "bytes=%d-%d", &from, &to); err != nil ||
				from != 2 || to != 5 {
				http.Error(w, "unexpected range "+rng, http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", from, to, len(sample)))
			w.Header().Set("Content-Length", strconv.Itoa(to-from+1))
			w.WriteHeader(http.StatusPartialContent)
			if r.Method != http.MethodHead {
				io.WriteString(w, sample[from:to+1])
			}
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(sample)))
		if r.Method != http.MethodHead {
			io.WriteString(w, sample)
		}
	}))
	defer upstream.Close()

	srv, err := startVideoServer()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.close()
	// Without a session the server gives nothing.
	noSession, err := http.Get(srv.streamURL(testVideoID))
	if err != nil {
		t.Fatal(err)
	}
	noSession.Body.Close()
	if noSession.StatusCode != http.StatusUnauthorized {
		t.Errorf("status %d without a session, want 401", noSession.StatusCode)
	}

	srv.setSession(upstream.URL, "token-123")
	stream := srv.streamURL(testVideoID)
	if got := stream[:len("http://127.0.0.1:")]; got != "http://127.0.0.1:" {
		t.Errorf("stream URL %q does not stay on the loopback interface", stream)
	}

	resp, err := http.Get(stream)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || string(body) != sample {
		t.Errorf("status %d with %q, want 200 with %q", resp.StatusCode, body, sample)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "video/mp4" {
		t.Errorf("Content-Type %q", ct)
	}
	if ar := resp.Header.Get("Accept-Ranges"); ar != "bytes" {
		t.Errorf("Accept-Ranges %q", ar)
	}

	// A player reads the stream in ranges.
	req, err := http.NewRequest(http.MethodGet, stream, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=2-5")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusPartialContent || string(body) != "2345" {
		t.Errorf("status %d with %q for a range, want 206 with %q", resp.StatusCode, body, "2345")
	}
	if cr := resp.Header.Get("Content-Range"); cr != "bytes 2-5/10" {
		t.Errorf("Content-Range %q", cr)
	}

	// A HEAD tells the length, without the body.
	req, err = http.NewRequest(http.MethodHead, stream, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || len(body) != 0 {
		t.Errorf("HEAD: status %d with %d bytes of body", resp.StatusCode, len(body))
	}

	// Nothing else is on the server.
	for _, path := range []string{
		"/",
		"/v",
		srv.prefix + "/",
		srv.prefix + "/not-a-uuid",
		srv.prefix + "/" + testVideoID + "/extra",
	} {
		resp, err := http.Get("http://" + srv.addr + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("status %d for %q, want 404", resp.StatusCode, path)
		}
	}
	resp, err = http.Post(stream, "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status %d for a POST, want 405", resp.StatusCode)
	}

	// Signing out stops the server from serving the session.
	srv.setSession("", "")
	resp, err = http.Get(stream)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status %d after signing out, want 401", resp.StatusCode)
	}
}

// TestVideoPageKeepsTheStillWithoutWindow checks what the image page shows
// for a video where no window plays it, as the tests run: its still and
// length, with no zoom, as a video is the player's to fit.
func TestVideoPageKeepsTheStillWithoutWindow(t *testing.T) {
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
	// The video is the second photo of the older day.
	if err := tt.Click("视频 2"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the video's still", func() bool {
		return a.main.viewer != nil && a.main.viewer.id == testVideoID &&
			a.main.viewer.bm != nil
	})
	tt.Frame()
	if !tt.HasText("视频") || !tt.HasText("0:42") {
		t.Errorf("texts %q on the video", tt.Texts())
	}
	if a.video.player != nil || a.video.err != nil || a.video.srv != nil {
		t.Errorf("video state without a window: %+v", a.video)
	}

	// No zoom of a video: not with the wheel, not with a double click, and
	// no bar to step it.
	tt.Wheel(600, 400, 0, -120)
	if z := a.main.viewer.zoomFactor(); z != 1 {
		t.Errorf("zoom %v after the wheel on a video, want 1", z)
	}
	tt.Advance(600 * time.Millisecond)
	tt.ClickAt(600, 400)
	tt.ClickAt(600, 400)
	tt.Frame()
	if z := a.main.viewer.zoomFactor(); z != 1 {
		t.Errorf("zoom %v after a double click on a video, want 1", z)
	}
	if tt.HasText("100%") || tt.HasText("适应窗口") {
		t.Errorf("texts %q on a video show the zoom bar", tt.Texts())
	}

	// The photo before it is an image again, at fit with its zoom bar.
	if err := tt.Click("上一张"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the image", func() bool {
		return a.main.viewer != nil && a.main.viewer.id == testOtherAssetID &&
			a.main.viewer.bm != nil
	})
	tt.Frame()
	if !tt.HasText("100%") {
		t.Errorf("texts %q back on an image", tt.Texts())
	}

	// Escape closes the page, and the video with it.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.main.viewer != nil {
		t.Error("the image page is still open after Escape")
	}
	if a.video.id != "" || a.video.player != nil || a.video.err != nil {
		t.Errorf("video state after leaving the page: %+v", a.video)
	}
}
