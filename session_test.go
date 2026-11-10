package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo"
)

// fakeSecrets is a credential store in memory, as mygo.Secrets is the
// system's.
type fakeSecrets map[string][]byte

func (f fakeSecrets) Get(key string) ([]byte, error) {
	v, ok := f[key]
	if !ok {
		return nil, mygo.ErrSecretNotFound
	}
	return v, nil
}

func (f fakeSecrets) Set(key string, value []byte) error {
	f[key] = append([]byte(nil), value...)
	return nil
}

func (f fakeSecrets) Delete(key string) error {
	if _, ok := f[key]; !ok {
		return mygo.ErrSecretNotFound
	}
	delete(f, key)
	return nil
}

func testSessionStore(t *testing.T) (sessionStore, fakeSecrets, string) {
	t.Helper()
	dir := t.TempDir()
	secrets := fakeSecrets{}
	store := sessionStore{
		secrets: secrets,
		dir:     func() (string, error) { return dir, nil },
	}
	return store, secrets, dir
}

func TestSessionStore(t *testing.T) {
	store, _, dir := testSessionStore(t)
	meta := storedSession{
		ServerURL: "https://photos.example.com",
		Version:   "2.1.0",
		UserID:    "user-1",
		UserName:  "Ada",
		UserEmail: "ada@example.com",
		IsAdmin:   true,
	}
	if err := store.save(meta, "token-123"); err != nil {
		t.Fatal(err)
	}

	got, err := store.loadMeta()
	if err != nil {
		t.Fatal(err)
	}
	if got != meta {
		t.Errorf("metadata %+v, want %+v", got, meta)
	}
	token, err := store.token()
	if err != nil {
		t.Fatal(err)
	}
	if token != "token-123" {
		t.Errorf("token %q, want token-123", token)
	}

	// The token belongs in the credential store, not the file.
	data, err := os.ReadFile(filepath.Join(dir, sessionFileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "token-123") {
		t.Error("the session file holds the token")
	}
}

func TestSessionStoreEmpty(t *testing.T) {
	store, _, _ := testSessionStore(t)
	meta, err := store.loadMeta()
	if err != nil {
		t.Fatal(err)
	}
	if meta.ServerURL != "" {
		t.Errorf("metadata %+v without a saved session", meta)
	}
	token, err := store.token()
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		t.Errorf("token %q without a saved session", token)
	}
}

func TestSessionStoreClearToken(t *testing.T) {
	store, _, _ := testSessionStore(t)
	meta := storedSession{ServerURL: "https://photos.example.com", UserEmail: "ada@example.com"}
	if err := store.save(meta, "token-123"); err != nil {
		t.Fatal(err)
	}
	if err := store.clearToken(); err != nil {
		t.Fatal(err)
	}
	token, err := store.token()
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		t.Errorf("token %q after clearing", token)
	}
	// Signing out keeps what prefills the next login.
	got, err := store.loadMeta()
	if err != nil {
		t.Fatal(err)
	}
	if got.UserEmail != meta.UserEmail {
		t.Errorf("metadata %+v after clearing the token", got)
	}
	// Clearing twice is fine.
	if err := store.clearToken(); err != nil {
		t.Errorf("clearing the token twice: %v", err)
	}
}

func TestSessionStoreReplacesToken(t *testing.T) {
	store, secrets, _ := testSessionStore(t)
	if err := store.save(storedSession{ServerURL: "https://old.example.com"}, "old-token"); err != nil {
		t.Fatal(err)
	}
	if err := store.save(storedSession{ServerURL: "https://new.example.com"}, "new-token"); err != nil {
		t.Fatal(err)
	}
	if string(secrets[accessTokenKey]) != "new-token" {
		t.Errorf("token %q after saving another session", secrets[accessTokenKey])
	}
	meta, err := store.loadMeta()
	if err != nil {
		t.Fatal(err)
	}
	if meta.ServerURL != "https://new.example.com" {
		t.Errorf("server %q after saving another session", meta.ServerURL)
	}
}

func TestSessionStoreRejectsBrokenFile(t *testing.T) {
	store, _, dir := testSessionStore(t)
	if err := os.WriteFile(filepath.Join(dir, sessionFileName), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.loadMeta(); err == nil {
		t.Fatal("loadMeta accepted a broken session file")
	}
}

// withTestSession points the app's session store at a temporary directory
// and an in-memory credential store for the test.
func withTestSession(t *testing.T) (fakeSecrets, string) {
	t.Helper()
	old := sessions
	dir := t.TempDir()
	secrets := fakeSecrets{}
	sessions = sessionStore{
		secrets: secrets,
		dir:     func() (string, error) { return dir, nil },
	}
	t.Cleanup(func() { sessions = old })
	return secrets, dir
}

func TestPrepareSession(t *testing.T) {
	withTestSession(t)
	meta := storedSession{
		ServerURL: "https://photos.example.com",
		Version:   "2.1.0",
		UserEmail: "ada@example.com",
	}
	if err := sessions.saveMeta(meta); err != nil {
		t.Fatal(err)
	}

	a := &app{}
	a.prepareSession()
	if a.login.stage != stageRestoring {
		t.Errorf("stage %d with a saved session, want restoring", a.login.stage)
	}
	if a.login.serverURL != meta.ServerURL || a.login.version != meta.Version ||
		a.login.email != meta.UserEmail {
		t.Errorf("login state %+v after preparing", a.login)
	}
}

func TestRestoreSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/timeline/buckets" {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `[]`)
			return
		}
		if r.URL.Path != "/api/users/me" ||
			r.Header.Get("Authorization") != "Bearer token-123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{
			"id":"3fa85f64-5717-4562-b3fc-2c963f66afa6",
			"email":"ada@example.com",
			"name":"Ada"
		}`)
	}))
	defer srv.Close()

	secrets, _ := withTestSession(t)
	secrets[accessTokenKey] = []byte("token-123")

	a := &app{}
	a.login.stage = stageRestoring
	a.login.serverURL = srv.URL
	a.restoreSession()

	if a.login.stage != stageSignedIn {
		t.Fatalf("stage %d after restoring a valid token, want signed in", a.login.stage)
	}
	if a.login.user == nil || a.login.user.Name != "Ada" ||
		a.login.user.Token != "token-123" {
		t.Errorf("account %+v after restoring", a.login.user)
	}
}

func TestRestoreSessionClearsExpiredToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	secrets, _ := withTestSession(t)
	secrets[accessTokenKey] = []byte("expired")

	a := &app{}
	a.login.stage = stageRestoring
	a.login.serverURL = srv.URL
	a.restoreSession()

	if a.login.stage != stageCredentials {
		t.Errorf("stage %d after an expired token, want credentials", a.login.stage)
	}
	if a.login.loginErr != "登录已过期，请重新登录" {
		t.Errorf("login error %q after an expired token", a.login.loginErr)
	}
	if _, ok := secrets[accessTokenKey]; ok {
		t.Error("the expired token is still saved")
	}
}

func TestRestoreSessionWithoutToken(t *testing.T) {
	withTestSession(t)
	a := &app{}
	a.login.stage = stageRestoring
	a.login.serverURL = "https://photos.example.com"
	a.restoreSession()

	if a.login.stage != stageCredentials {
		t.Errorf("stage %d without a token, want credentials", a.login.stage)
	}
	if a.login.user != nil || a.login.loginErr != "" {
		t.Errorf("login state %+v without a token", a.login)
	}
}
