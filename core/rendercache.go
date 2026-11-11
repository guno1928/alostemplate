package core

import "sync/atomic"

const (
	renderCacheWays    = 2
	renderCacheBuckets = 256
	renderCacheSlots   = renderCacheBuckets * renderCacheWays
	renderCacheShift   = 64 - 8
)

type renderCacheEntry struct {
	key     uint64
	expires int64
	out     string
}

type renderCache struct {
	slots [renderCacheSlots]atomic.Pointer[renderCacheEntry]
}

func renderCacheBucket(key uint64) uint64 {
	return (key >> renderCacheShift) * renderCacheWays
}

func (e *renderCacheEntry) serves(key uint64, now int64) bool {
	return e != nil && e.key == key && now < e.expires
}

func (e *renderCacheEntry) replaceableBy(key uint64, now int64) bool {
	return e == nil || e.key == key || now >= e.expires
}

func (c *renderCache) load(key uint64, now int64) (string, bool) {
	bucket := renderCacheBucket(key)
	if e := c.slots[bucket].Load(); e.serves(key, now) {
		return e.out, true
	}
	if e := c.slots[bucket+1].Load(); e.serves(key, now) {
		return e.out, true
	}
	return "", false
}

func (c *renderCache) store(key uint64, out string, now int64, expires int64) {
	entry := &renderCacheEntry{key: key, expires: expires, out: out}
	bucket := renderCacheBucket(key)
	first := c.slots[bucket].Load()
	if first.replaceableBy(key, now) {
		c.slots[bucket].Store(entry)
		return
	}
	second := c.slots[bucket+1].Load()
	if second.replaceableBy(key, now) || second.expires <= first.expires {
		c.slots[bucket+1].Store(entry)
		return
	}
	c.slots[bucket].Store(entry)
}
