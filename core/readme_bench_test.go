package core

import (
	"strconv"
	"testing"
)

var readmeSlotCounts = []int{0, 1, 4, 8, 16, 64}

func BenchmarkREADME_CacheHit_Map(b *testing.B) {
	for _, n := range readmeSlotCounts {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			e := newTestEngine(b)
			tpl := mustCompile(b, e, buildSource(n, 24, "k"))
			values := buildValues(n, "k", 12)
			warm := ReplaceMap(tpl, values)
			b.ReportAllocs()
			b.SetBytes(int64(len(warm)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sinkString = ReplaceMap(tpl, values)
			}
		})
	}
}

func BenchmarkREADME_CacheHit_Pairs(b *testing.B) {
	for _, n := range readmeSlotCounts {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			e := newTestEngine(b)
			tpl := mustCompile(b, e, buildSource(n, 24, "k"))
			pairs := buildPairs(n, "k", 12)
			warm := Replace(tpl, pairs)
			b.ReportAllocs()
			b.SetBytes(int64(len(warm)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sinkString = Replace(tpl, pairs)
			}
		})
	}
}

func BenchmarkREADME_CacheMiss_Map(b *testing.B) {
	for _, n := range readmeSlotCounts {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			e := newTestEngine(b, WithRenderCache(RenderCacheDisabled))
			tpl := mustCompile(b, e, buildSource(n, 24, "k"))
			values := buildValues(n, "k", 12)
			warm := ReplaceMap(tpl, values)
			b.ReportAllocs()
			b.SetBytes(int64(len(warm)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sinkString = ReplaceMap(tpl, values)
			}
		})
	}
}

func BenchmarkREADME_PageSizeCacheHit(b *testing.B) {
	for _, size := range []int{1024, 8192, 32768, 131072} {
		b.Run(strconv.Itoa(size/1024)+"KB", func(b *testing.B) {
			e := newTestEngine(b)
			src, value := singleSlotPage(size, 32)
			tpl := mustCompile(b, e, src)
			pairs := []string{value}
			warm := Replace(tpl, pairs)
			b.ReportAllocs()
			b.SetBytes(int64(len(warm)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sinkString = Replace(tpl, pairs)
			}
		})
	}
}

func BenchmarkREADME_PageSizeCacheMiss(b *testing.B) {
	for _, size := range []int{1024, 8192, 32768, 131072} {
		b.Run(strconv.Itoa(size/1024)+"KB", func(b *testing.B) {
			e := newTestEngine(b, WithRenderCache(RenderCacheDisabled))
			src, value := singleSlotPage(size, 32)
			tpl := mustCompile(b, e, src)
			pairs := []string{value}
			warm := Replace(tpl, pairs)
			b.ReportAllocs()
			b.SetBytes(int64(len(warm)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sinkString = Replace(tpl, pairs)
			}
		})
	}
}
