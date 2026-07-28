package core

import (
	"runtime"
	"sync"
	"testing"
)

// These benchmarks measure what a "pre-warmed pool" could actually buy. The
// render scratch pool is only reached by templates above the inline limits, and
// sync.Pool is drained by the garbage collector, so both facts bound the win.

func poolBenchTemplate(b *testing.B, slots int) (*Template, []string) {
	b.Helper()
	e := NewEngine()
	b.Cleanup(e.Stop)
	src := buildSource(slots, 24, "key")
	tpl, err := e.compileSource(src)
	if err != nil {
		b.Fatalf("compile: %v", err)
	}
	return tpl, buildPairs(slots, "key", 12)
}

// BenchmarkPoolSteadyState is the best case: the pool already holds a scratch
// object sized for this template.
func BenchmarkPoolSteadyState(b *testing.B) {
	tpl, pairs := poolBenchTemplate(b, 64)
	dst := make([]byte, 0, 1<<16)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dst = Replace(tpl, dst, pairs)
	}
	benchSink = dst
}

// BenchmarkPoolAfterGC forces a collection every iteration, which is what
// actually empties a sync.Pool in a running process.
func BenchmarkPoolAfterGC(b *testing.B) {
	tpl, pairs := poolBenchTemplate(b, 64)
	dst := make([]byte, 0, 1<<16)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Exclude the collection itself so the number reported is the cost of
		// the first render against an emptied pool, not the cost of GC.
		b.StopTimer()
		runtime.GC()
		runtime.GC()
		b.StartTimer()
		dst = Replace(tpl, dst, pairs)
	}
	benchSink = dst
}

// BenchmarkPoolFirstTouchPerP measures a render from many goroutines at once,
// which is where a pool pre-warmed on a single P stops helping: sync.Pool is
// per-P, so objects placed by one goroutine are not visible to the others.
func BenchmarkPoolFirstTouchPerP(b *testing.B) {
	tpl, pairs := poolBenchTemplate(b, 64)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		dst := make([]byte, 0, 1<<16)
		for pb.Next() {
			dst = Replace(tpl, dst, pairs)
		}
		benchSink = dst
	})
}

// TestPoolIsDrainedByGC records the behaviour that makes "always pre-warmed"
// unachievable with sync.Pool: a collection discards pooled entries.
func TestPoolIsDrainedByGC(t *testing.T) {
	var pool sync.Pool
	pool.Put(new(renderScratch))
	// One GC moves the primary cache to the victim cache; a second discards it.
	runtime.GC()
	runtime.GC()
	if got := pool.Get(); got != nil {
		t.Skip("runtime retained the pooled value; sync.Pool draining is best effort")
	}
}

// TestInlinePathAvoidsPoolEntirely documents that ordinary templates never reach
// the pool, so pre-warming cannot affect them.
func TestInlinePathAvoidsPoolEntirely(t *testing.T) {
	e := newTestEngine(t)
	for _, slots := range []int{1, 2, 4, 8, 16} {
		src := buildSource(slots, 12, "key")
		tpl := mustCompile(t, e, src)
		if len(tpl.keys) > inlineKeyLimit || 2*len(tpl.slots)+1 > inlinePartLimit {
			t.Fatalf("slots=%d unexpectedly exceeds the inline limits", slots)
		}
	}
	// 17 keys crosses the limit and does reach the pool.
	src := buildSource(17, 12, "key")
	tpl := mustCompile(t, e, src)
	if len(tpl.keys) <= inlineKeyLimit {
		t.Fatal("expected 17 keys to exceed the inline key limit")
	}
}
