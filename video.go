package main

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/video"
	"github.com/google/uuid"
)

// videoServer is the loopback HTTP server that lets the system's video
// player read an asset of Immich: the player asks for a plain URL, without
// the session's token, and the server adds the token to what it asks of
// Immich, passing the media's ranges and lengths back. It listens on the
// loopback interface only, under a path named by a random secret, so that
// nothing else on the machine can read the stream through it.
type videoServer struct {
	// addr is the host and port it listens on; prefix is the path the
	// secret names.
	addr   string
	prefix string
	http   *http.Server

	// mu guards the session the server serves, which signing in sets and
	// signing out clears.
	mu    sync.Mutex
	base  string
	token string
}

// videoHTTP is the client the server reads the streams with. Compression
// stays off: the ranges a player asks for count the bytes of the file, so
// the body must come as Immich sends it.
var videoHTTP = func() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DisableCompression = true
	return &http.Client{Transport: tr}
}()

// videoMediaHeaders are what Immich says of a stream that a player needs:
// its type and length, the range it answered, and how long the file stays
// fresh.
var videoMediaHeaders = []string{
	"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges",
	"Content-Encoding", "ETag", "Last-Modified", "Cache-Control",
}

// startVideoServer starts the server on a port of its own.
func startVideoServer() (*videoServer, error) {
	var secret [16]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &videoServer{
		addr:   ln.Addr().String(),
		prefix: "/v/" + hex.EncodeToString(secret[:]),
	}
	s.http = &http.Server{Handler: http.HandlerFunc(s.handle)}
	go s.http.Serve(ln)
	return s, nil
}

// streamURL is where the player reads the video of an asset.
func (s *videoServer) streamURL(id string) string {
	return "http://" + s.addr + s.prefix + "/" + id
}

// setSession tells the server whose session it serves: the address of the
// Immich server and the token of the account. An empty token stops it from
// serving.
func (s *videoServer) setSession(base, token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.base = strings.TrimRight(base, "/")
	s.token = token
}

// close stops the server, and the streams on it.
func (s *videoServer) close() {
	s.setSession("", "")
	s.http.Close()
}

// handle serves one request of a player: the stream of the asset its path
// names. Nothing else is on the server.
func (s *videoServer) handle(w http.ResponseWriter, r *http.Request) {
	id, ok := strings.CutPrefix(r.URL.Path, s.prefix+"/")
	if !ok || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	if _, err := uuid.Parse(id); err != nil {
		http.NotFound(w, r)
		return
	}
	s.relay(w, r, id)
}

// relay asks Immich for the stream of the asset, with the session's token,
// and passes its answer back as it is, ranges and all.
func (s *videoServer) relay(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	base, token := s.base, s.token
	s.mu.Unlock()
	if base == "" || token == "" {
		http.Error(w, "no session", http.StatusUnauthorized)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method,
		base+"/api/assets/"+id+"/video/playback", nil)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	// A player reads the stream in ranges, from any part of the file.
	if rng := r.Header.Get("Range"); rng != "" {
		req.Header.Set("Range", rng)
	}
	if ir := r.Header.Get("If-Range"); ir != "" {
		req.Header.Set("If-Range", ir)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "*/*")
	resp, err := videoHTTP.Do(req)
	if err != nil {
		http.Error(w, "upstream: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for _, h := range videoMediaHeaders {
		if v := resp.Header.Values(h); len(v) > 0 {
			w.Header()[h] = v
		}
	}
	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodHead {
		return
	}
	io.Copy(w, resp.Body)
}

// videoState is the image page's video: the loopback server the system's
// player reads through, and the player of the asset shown, once it opens.
type videoState struct {
	srv    *videoServer
	player *ui.VideoPlayer
	// id is the asset the player plays, or was meant to; err what stopped
	// it from opening. muted is the sound the user chose, which the next
	// video keeps.
	id    string
	err   error
	muted bool
}

// playVideo opens the native player of a video asset, closing the last one.
// The page keeps the still where the app runs without a window, as the
// tests do.
func (a *app) playVideo(id string) {
	if a.video.id == id && (a.video.player != nil || a.video.err != nil) {
		return
	}
	a.stopVideo()
	if a.win == nil || a.login.user == nil || a.login.user.Token == "" {
		return
	}
	srv, err := a.videoServer()
	if err != nil {
		a.video.id, a.video.err = id, err
		return
	}
	srv.setSession(a.login.serverURL, a.login.user.Token)
	p, err := video.OpenURL(srv.streamURL(id))
	a.video.id = id
	if err != nil {
		a.video.err = err
		return
	}
	p.SetMuted(a.video.muted)
	a.video.player = p
}

// toggleVideoMuted turns the video's sound off, or on again, keeping the
// choice for the videos shown after it.
func (a *app) toggleVideoMuted() {
	p := a.video.player
	if p == nil {
		return
	}
	a.video.muted = !p.Muted()
	p.SetMuted(a.video.muted)
}

// videoServer starts the loopback server the first time a video opens.
func (a *app) videoServer() (*videoServer, error) {
	if a.video.srv != nil {
		return a.video.srv, nil
	}
	srv, err := startVideoServer()
	if err != nil {
		return nil, err
	}
	a.video.srv = srv
	return srv, nil
}

// retryVideo opens the video shown again, after it failed.
func (a *app) retryVideo() {
	id := a.video.id
	if id == "" {
		if v := a.main.viewer; v != nil {
			id = v.id
		}
	}
	a.stopVideo()
	a.playVideo(id)
}

// stopVideo closes the player, and its sound. The server stays for the next
// video that opens.
func (a *app) stopVideo() {
	if p := a.video.player; p != nil {
		p.Close()
	}
	a.video.player = nil
	a.video.id = ""
	a.video.err = nil
	// The still shows again, and a press waiting to play its motion starts
	// over.
	if v := a.main.viewer; v != nil {
		v.liveOn = false
		v.liveAt = time.Time{}
	}
}

// closeVideoServer stops the player and the server, as signing out does: the
// next session starts its own, with its own secret.
func (a *app) closeVideoServer() {
	a.stopVideo()
	if srv := a.video.srv; srv != nil {
		srv.close()
		a.video.srv = nil
	}
}
