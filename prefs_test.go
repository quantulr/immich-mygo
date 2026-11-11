package main

import "testing"

// TestPrefsStore keeps the interface's choices in the user data directory.
func TestPrefsStore(t *testing.T) {
	dir := t.TempDir()
	s := prefsStore{dir: func() (string, error) { return dir, nil }}

	p, err := s.load()
	if err != nil || p.DetailsOpen {
		t.Fatalf("preferences before a save %+v, %v", p, err)
	}
	if err := s.save(preferences{DetailsOpen: true}); err != nil {
		t.Fatal(err)
	}
	p, err = s.load()
	if err != nil || !p.DetailsOpen {
		t.Fatalf("preferences after a save %+v, %v", p, err)
	}
}
