package main

import (
	"image"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestThumbCache keeps the thumbnails shown lately within the budget, and
// lets the pixels of the ones not shown for longest go.
func TestThumbCache(t *testing.T) {
	c := &thumbCache{budget: 8}
	newPhoto := func() *photo {
		return &photo{Bm: ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 1, 1)))}
	}
	first, second, third := newPhoto(), newPhoto(), newPhoto()
	c.put(first)
	c.put(second)

	// What the timeline shows again is the last to go.
	c.touch(first)
	c.put(third)
	if first.Bm == nil {
		t.Error("the touched thumbnail lost its pixels")
	}
	if third.Bm == nil {
		t.Error("the newest thumbnail lost its pixels")
	}
	if second.Bm != nil {
		t.Error("the thumbnail not shown lately kept its pixels")
	}

	// The end of the session lets all of them go.
	c.clear()
	if first.Bm != nil || third.Bm != nil {
		t.Error("a thumbnail kept its pixels after the cache cleared")
	}
	c.clear()
}
