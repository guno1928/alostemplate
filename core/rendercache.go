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
	slots    [renderCacheSlots]atomic.Pointer[renderCacheEntry]
	seenOnce [renderCacheSlots]atomic.Uint64
}

func (c *renderCache) seenBefore(key uint64) bool {
	seen := &c.seenOnce[key&(renderCacheSlots-1)]
	if seen.Load() == key {
		return true
	}
	seen.Store(key)
	return false
}

func renderCacheBucket(key uint64) uint64 {
	return (key >> renderCacheShift) * renderCacheWays
}

func (e *renderCacheEntry) replaceableBy(key uint64, now int64) bool {
	return e == nil || e.key == key || now >= e.expires
}

func (c *renderCache) load(key uint64, now int64) (string, bool) {
	bucket := renderCacheBucket(key)
	e := c.slots[bucket].Load()
	if e == nil || e.key != key || now >= e.expires {
		e = c.slots[bucket+1].Load()
		if e == nil || e.key != key || now >= e.expires {
			return "", false
		}
	}
	return e.out, true
}

func (c *renderCache) store(key uint64, out string, now int64, expires int64) {
	if !c.seenBefore(key) {
		return
	}
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
