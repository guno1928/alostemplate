package core

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func cacheTestTemplate(t testing.TB, e *Engine, slots int) *Template {
	t.Helper()
	return mustCompile(t, e, buildSource(slots, 64, "k"))
}

func TestCachedReplaceMapKeyedByValues(t *testing.T) {
	e := newTestEngine(t)
	tpl := cacheTestTemplate(t, e, 4)

	a := string(ReplaceMap(tpl, nil, map[string]string{"k0": "AAA", "k1": "BBB", "k2": "C", "k3": "D"}))
	b := string(ReplaceMap(tpl, nil, map[string]string{"k0": "ZZZ", "k1": "YYY", "k2": "X", "k3": "W"}))

	if a == b {
		t.Fatal("different values produced identical output")
	}
	if !strings.Contains(a, "AAA") || strings.Contains(a, "ZZZ") {
		t.Fatalf("first render leaked or lost values: %q", a)
	}
	if !strings.Contains(b, "ZZZ") || strings.Contains(b, "AAA") {
		t.Fatalf("second render leaked or lost values: %q", b)
	}
}

func TestCachedAndUncachedRendersAgree(t *testing.T) {
	cached := newTestEngine(t)
	uncached := newTestEngine(t, WithRenderCache(RenderCacheDisabled))
	for _, slots := range []int{0, 1, 2, 8, 16, 64} {
		src := buildSource(slots, 64, "k")
		tplCached := mustCompile(t, cached, src)
		tplUncached := mustCompile(t, uncached, src)
		for _, count := range []int{0, 1, slots} {
			values := buildValues(count, "k", 12)
			want := string(ReplaceMap(tplUncached, nil, values))
			for i := 0; i < 3; i++ {
				if got := string(ReplaceMap(tplCached, nil, values)); got != want {
					t.Fatalf("slots=%d count=%d call=%d: cached differs from uncached", slots, count, i)
				}
			}

			pairs := buildPairs(count, "k", 12)
			wantPairs := string(Replace(tplUncached, nil, pairs))
			for i := 0; i < 3; i++ {
				if got := string(Replace(tplCached, nil, pairs)); got != wantPairs {
					t.Fatalf("slots=%d count=%d call=%d: cached pairs differ", slots, count, i)
				}
			}
		}
	}
}

func TestCachedReplaceHonoursCallerBuffer(t *testing.T) {
	e := newTestEngine(t)
	tpl := cacheTestTemplate(t, e, 4)
	values := buildValues(4, "k", 12)

	want := string(ReplaceMap(tpl, nil, values))

	dst := make([]byte, 0, 8192)
	got := ReplaceMap(tpl, dst, values)
	if string(got) != want {
		t.Fatal("render into caller buffer differs from cached render")
	}
	if cap(got) != cap(dst) || &got[:1][0] != &dst[:1][0] {
		t.Fatal("a caller-supplied buffer must be written into, not replaced by a cached one")
	}
}

func TestCachedRenderNoCollisionAcrossManyValueSets(t *testing.T) {
	e := newTestEngine(t)
	tpl := cacheTestTemplate(t, e, 4)
	seen := make(map[string]string, 2000)
	for i := 0; i < 2000; i++ {
		id := "id-" + strconv.Itoa(i)
		out := string(ReplaceMap(tpl, nil, map[string]string{
			"k0": id, "k1": strconv.Itoa(i), "k2": "x", "k3": "y",
		}))
		if !strings.Contains(out, id) {
			t.Fatalf("render for %s does not contain its own value", id)
		}
		if prev, dup := seen[out]; dup {
			t.Fatalf("%s and %s produced identical output: cache collision", prev, id)
		}
		seen[out] = id
	}
	if len(seen) != 2000 {
		t.Fatalf("expected 2000 distinct renders, got %d", len(seen))
	}
}

func TestRenderCacheDefaultTTL(t *testing.T) {
	e := newTestEngine(t)
	if got := e.RenderCacheTTL(); got != DefaultRenderCacheTTL {
		t.Fatalf("default render cache TTL = %v, want %v", got, DefaultRenderCacheTTL)
	}
	e2 := newTestEngine(t, WithRenderCache(RenderCacheDisabled))
	if got := e2.RenderCacheTTL(); got != 0 {
		t.Fatalf("disabled render cache TTL = %v, want 0", got)
	}
}

func TestRenderCacheClearedOnReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "page.alos")
	if err := os.WriteFile(path, []byte("VERSION-ONE {{k0}}"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := newTestEngine(t)
	tpl, err := e.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"k0": "v"}
	if first := string(ReplaceMap(tpl, nil, values)); !strings.Contains(first, "VERSION-ONE") {
		t.Fatalf("unexpected first render: %q", first)
	}

	if err := os.WriteFile(path, []byte("VERSION-TWO {{k0}}"), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Second)
	os.Chtimes(path, future, future)
	if err := e.Reload(); err != nil {
		t.Fatal(err)
	}

	if second := string(ReplaceMap(tpl, nil, values)); !strings.Contains(second, "VERSION-TWO") {
		t.Fatalf("reload did not invalidate the render cache: got %q", second)
	}
}

func TestClearRenderCacheForcesRerender(t *testing.T) {
	e := newTestEngine(t)
	tpl := cacheTestTemplate(t, e, 4)
	values := buildValues(4, "k", 12)
	want := string(ReplaceMap(tpl, nil, values))
	tpl.ClearRenderCache()
	if got := string(ReplaceMap(tpl, nil, values)); got != want {
		t.Fatal("render after ClearRenderCache differs")
	}
}

func TestCachedRenderConcurrentCorrectness(t *testing.T) {
	e := newTestEngine(t)
	tpl := cacheTestTemplate(t, e, 4)
	var wg sync.WaitGroup
	errs := make(chan string, 32)
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			id := "worker-" + strconv.Itoa(w)
			for i := 0; i < 200; i++ {
				out := string(ReplaceMap(tpl, nil, map[string]string{
					"k0": id, "k1": "b", "k2": "c", "k3": "d",
				}))
				if !strings.Contains(out, id) {
					errs <- "worker " + id + " saw another worker's render"
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Fatal(msg)
	}
}

func TestCachedRenderNilTemplate(t *testing.T) {
	var tpl *Template
	if got := ReplaceMap(tpl, nil, nil); len(got) != 0 {
		t.Fatalf("nil template should render empty, got %q", got)
	}
	if got := Replace(tpl, nil, nil); len(got) != 0 {
		t.Fatalf("nil template should render empty, got %q", got)
	}
	tpl.ClearRenderCache()
}

func BenchmarkCachedReplaceMap_Hit(b *testing.B) {
	e := newTestEngine(b)
	tpl := mustCompile(b, e, buildSource(16, 512, "k"))
	values := buildValues(16, "k", 24)
	warm := ReplaceMap(tpl, nil, values)
	b.ReportAllocs()
	b.SetBytes(int64(len(warm)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkBytes = ReplaceMap(tpl, nil, values)
	}
}

func BenchmarkCachedReplaceMap_Disabled(b *testing.B) {
	e := newTestEngine(b, WithRenderCache(RenderCacheDisabled))
	tpl := mustCompile(b, e, buildSource(16, 512, "k"))
	values := buildValues(16, "k", 24)
	warm := ReplaceMap(tpl, nil, values)
	b.ReportAllocs()
	b.SetBytes(int64(len(warm)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkBytes = ReplaceMap(tpl, nil, values)
	}
}

func BenchmarkCachedReplaceMap_HitParallel(b *testing.B) {
	e := newTestEngine(b)
	tpl := mustCompile(b, e, buildSource(16, 512, "k"))
	values := buildValues(16, "k", 24)
	warm := ReplaceMap(tpl, nil, values)
	b.ReportAllocs()
	b.SetBytes(int64(len(warm)))
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			sinkBytes = ReplaceMap(tpl, nil, values)
		}
	})
}
