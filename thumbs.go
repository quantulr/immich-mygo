package main

import (
	"container/list"
	"sync"
)

// thumbBudget is how much decoded thumbnail memory the timeline keeps, in
// bytes. The thumbnails shown lately stay; the ones not shown for longest
// let their pixels go, so a timeline of any size stays within it.
const thumbBudget = 128 << 20

// thumbLoads limits the thumbnails loading at once. Their fetch and decode
// is the work a long timeline makes, and a fast scroll would otherwise
// start hundreds of them.
var thumbLoads = make(chan struct{}, 4)

// thumbCache keeps the thumbnails that have loaded, the ones shown lately,
// within a memory budget. It is guarded by a mutex because the loads
// publish through app.update, which the tests run on their goroutines.
type thumbCache struct {
	mu     sync.Mutex
	budget int64
	used   int64
	order  *list.List
	elems  map[*photo]*list.Element
}

// thumbItem is a cached thumbnail: the photo it belongs to, and the bytes
// its pixels take.
type thumbItem struct {
	p    *photo
	size int64
}

// put keeps p's thumbnail, and lets the pixels of those not shown for
// longest go once the cache is past its budget.
func (c *thumbCache) put(p *photo) {
	if p.Bm == nil {
		return
	}
	w, h := p.Bm.Size()
	size := int64(w) * int64(h) * 4

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.order == nil {
		c.order = list.New()
		c.elems = make(map[*photo]*list.Element)
	}
	if e, ok := c.elems[p]; ok {
		item := e.Value.(*thumbItem)
		c.used += size - item.size
		item.size = size
		c.order.MoveToFront(e)
	} else {
		c.elems[p] = c.order.PushFront(&thumbItem{p: p, size: size})
		c.used += size
	}
	budget := c.budget
	if budget <= 0 {
		budget = thumbBudget
	}
	for c.used > budget {
		e := c.order.Back()
		if e == nil {
			break
		}
		item := e.Value.(*thumbItem)
		c.order.Remove(e)
		delete(c.elems, item.p)
		c.used -= item.size
		item.p.Bm.ReleasePixels()
		item.p.Bm = nil
	}
}

// clear lets every thumbnail's pixels go, as when the session ends.
func (c *thumbCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.elems == nil {
		return
	}
	for e := c.order.Back(); e != nil; e = e.Prev() {
		item := e.Value.(*thumbItem)
		if item.p.Bm != nil {
			item.p.Bm.ReleasePixels()
			item.p.Bm = nil
		}
	}
	c.order.Init()
	clear(c.elems)
	c.used = 0
}

// touch notes that p's thumbnail is on screen now, so that it is the last
// to let its pixels go.
func (c *thumbCache) touch(p *photo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.elems == nil {
		return
	}
	if e, ok := c.elems[p]; ok {
		c.order.MoveToFront(e)
	}
}
