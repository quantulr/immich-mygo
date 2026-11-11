package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
)

// prefsFileName is the file in the user data directory that keeps the
// interface's choices between runs.
const prefsFileName = "prefs.json"

// preferences are the interface's choices that outlast a run.
type preferences struct {
	// DetailsOpen shows the image page's details as it opens.
	DetailsOpen bool `json:"detailsOpen,omitempty"`
}

// prefsStore keeps the interface's choices in the user data directory.
type prefsStore struct {
	// dir returns the user data directory, PathUserData in the app.
	dir func() (string, error)
}

// appPrefs is the app's store.
var appPrefs = prefsStore{
	dir: func() (string, error) { return mygo.App.Path(mygo.PathUserData) },
}

// load returns the choices of the last run, or the zero choices when there
// are none.
func (s prefsStore) load() (preferences, error) {
	var p preferences
	path, err := s.path()
	if err != nil {
		return p, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return preferences{}, err
	}
	return p, nil
}

// save writes the choices for the next run, replacing the file with a
// sibling renamed over it.
func (s prefsStore) save(p preferences) error {
	data, err := json.MarshalIndent(p, "", "  ")
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

// path is the file that keeps the choices.
func (s prefsStore) path() (string, error) {
	dir, err := s.dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, prefsFileName), nil
}
