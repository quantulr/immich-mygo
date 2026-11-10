package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
)

const (
	// accessTokenKey is the key of the session token in the system's
	// credential store.
	accessTokenKey = "access-token"
	// sessionFileName is the file in the user data directory that keeps
	// what identifies the session. The token is not in it.
	sessionFileName = "session.json"
)

// storedSession is the part of a session the app keeps between runs: the
// server and who signed in on it. The token goes to the system's
// credential store instead.
type storedSession struct {
	ServerURL string `json:"serverUrl"`
	Version   string `json:"version,omitempty"`
	UserID    string `json:"userId,omitempty"`
	UserName  string `json:"userName,omitempty"`
	UserEmail string `json:"userEmail,omitempty"`
	IsAdmin   bool   `json:"isAdmin,omitempty"`
}

// secretStore is the part of mygo.SecretsModule the session store uses:
// the system's credential store, or a fake in tests.
type secretStore interface {
	Get(key string) ([]byte, error)
	Set(key string, value []byte) error
	Delete(key string) error
}

// sessionStore keeps a session between runs: the token in the system's
// credential store, the rest in a file of the user data directory.
type sessionStore struct {
	secrets secretStore
	// dir returns the user data directory, PathUserData in the app.
	dir func() (string, error)
}

// sessions is the app's store. The token is encrypted by the system, as
// a file in the user data directory is not.
var sessions = sessionStore{
	secrets: mygo.Secrets,
	dir:     func() (string, error) { return mygo.App.Path(mygo.PathUserData) },
}

// loadMeta returns the session the last run left, without its token, or
// the zero session when there is none.
func (s sessionStore) loadMeta() (storedSession, error) {
	path, err := s.path()
	if err != nil {
		return storedSession{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return storedSession{}, nil
	}
	if err != nil {
		return storedSession{}, err
	}
	var meta storedSession
	if err := json.Unmarshal(data, &meta); err != nil {
		return storedSession{}, err
	}
	return meta, nil
}

// token returns the saved token, or "" when there is none.
func (s sessionStore) token() (string, error) {
	data, err := s.secrets.Get(accessTokenKey)
	if errors.Is(err, mygo.ErrSecretNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// save stores meta and token for the next run. The token of the session
// being replaced goes first, so that a failure leaves the metadata alone
// rather than a token that belongs to another session.
func (s sessionStore) save(meta storedSession, token string) error {
	if err := s.clearToken(); err != nil {
		return err
	}
	if err := s.saveMeta(meta); err != nil {
		return err
	}
	return s.secrets.Set(accessTokenKey, []byte(token))
}

// saveMeta writes meta to the user data directory, replacing the file
// with a sibling renamed over it.
func (s sessionStore) saveMeta(meta storedSession) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	path, err := s.path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// clearToken removes the saved token, keeping what identifies the
// session for the next run. Removing a token that is not there is fine.
func (s sessionStore) clearToken() error {
	err := s.secrets.Delete(accessTokenKey)
	if errors.Is(err, mygo.ErrSecretNotFound) {
		return nil
	}
	return err
}

// path is the file that keeps the session's metadata.
func (s sessionStore) path() (string, error) {
	dir, err := s.dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, sessionFileName), nil
}
