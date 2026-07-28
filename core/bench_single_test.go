package core

import (
	"strconv"
	"strings"
	"testing"
)

// Benchmarks for the realistic "one placeholder in a real HTML page" case. This
// hits the single-slot fast path, which is three memmoves (prefix, value,
// suffix), so the cost tracks page size rather than template complexity.

func singleSlotPage(totalBytes int, valueLen int) (src string, value string) {
	const marker = "{{content}}"
	body := totalBytes - len(marker)
	if body < 0 {
		body = 0
	}
	head := strings.Repeat("<div class=\"row\">filler markup</div>", body/36/2+1)
	tail := strings.Repeat("<p>trailing paragraph content here</p>", body/38/2+1)
	src = head + marker + tail
	value = strings.Repeat("X", valueLen)
	return
}

func benchSinglePage(b *testing.B, totalBytes int, valueLen int) {
	e := NewEngine()
	defer e.Stop()
	src, value := singleSlotPage(totalBytes, valueLen)
	tpl, err := e.compileSource(src)
	if err != nil {
		b.Fatalf("compile: %v", err)
	}
	if !tpl.single.enabled {
		b.Fatal("expected the single-slot fast path")
	}
	out := len(src) - len("{{content}}") + valueLen

	b.Run("warm_shorthand", func(b *testing.B) {
		dst := make([]byte, 0, out+64)
		pairs := []string{value}
		b.SetBytes(int64(out))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			sinkString = Replace(tpl, pairs)
		}
		benchSink = dst
	})
	b.Run("warm_keyed", func(b *testing.B) {
		dst := make([]byte, 0, out+64)
		pairs := []string{"content", value}
		b.SetBytes(int64(out))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			sinkString = Replace(tpl, pairs)
		}
		benchSink = dst
	})
	b.Run("warm_map", func(b *testing.B) {
		dst := make([]byte, 0, out+64)
		values := map[string]string{"content": value}
		b.SetBytes(int64(out))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			sinkString = ReplaceMap(tpl, values)
		}
		benchSink = dst
	})
	b.Run("cold_alloc", func(b *testing.B) {
		pairs := []string{value}
		b.SetBytes(int64(out))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			sinkString = Replace(tpl, pairs)
		}
	})
}

func BenchmarkSinglePage1KB(b *testing.B)   { benchSinglePage(b, 1024, 32) }
func BenchmarkSinglePage8KB(b *testing.B)   { benchSinglePage(b, 8*1024, 64) }
func BenchmarkSinglePage32KB(b *testing.B)  { benchSinglePage(b, 32*1024, 128) }
func BenchmarkSinglePage128KB(b *testing.B) { benchSinglePage(b, 128*1024, 256) }

// BenchmarkSinglePageParallel measures the same page rendered concurrently, the
// shape a web server actually runs.
func BenchmarkSinglePageParallel(b *testing.B) {
	e := NewEngine()
	defer e.Stop()
	for _, size := range []int{1024, 8 * 1024, 32 * 1024} {
		src, value := singleSlotPage(size, 64)
		tpl, err := e.compileSource(src)
		if err != nil {
			b.Fatalf("compile: %v", err)
		}
		out := len(src) - len("{{content}}") + 64
		b.Run(strconv.Itoa(size/1024)+"KB", func(b *testing.B) {
			pairs := []string{value}
			b.SetBytes(int64(out))
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				dst := make([]byte, 0, out+64)
				for pb.Next() {
					sinkString = Replace(tpl, pairs)
				}
				benchSink = dst
			})
		})
	}
}

func TestSingleSlotPageRendersCorrectly(t *testing.T) {
	e := newTestEngine(t)
	for _, size := range []int{256, 1024, 8 * 1024, 64 * 1024} {
		src, value := singleSlotPage(size, 48)
		tpl := mustCompile(t, e, src)
		if !tpl.single.enabled {
			t.Fatalf("size=%d did not take the single-slot path", size)
		}
		want := strings.Replace(src, "{{content}}", value, 1)
		if got := Replace(tpl, []string{value}); got != want {
			t.Fatalf("size=%d shorthand render mismatch", size)
		}
		if got := Replace(tpl, []string{"content", value}); got != want {
			t.Fatalf("size=%d keyed render mismatch", size)
		}
		if got := ReplaceMap(tpl, map[string]string{"content": value}); got != want {
			t.Fatalf("size=%d map render mismatch", size)
		}
	}
}
