package core

import (
	"strconv"
	"testing"
)

// realPageSizes spans the sizes actually rendered by alossite (75KB-221KB)
// plus the sizes at which 150M pages/sec is still reachable.
var realPageSizes = []int{2048, 4096, 5120, 8192, 75000, 111000, 150000, 221000}

func BenchmarkRealSizeStaticParallel(b *testing.B) {
	for _, size := range realPageSizes {
		b.Run(strconv.Itoa(size)+"B", func(b *testing.B) {
			e := newTestEngine(b)
			tpl := mustCompile(b, e, buildSource(0, size, "k"))
			warm := tpl.renderStatic(nil)
			b.ReportAllocs()
			b.SetBytes(int64(len(warm)))
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				var dst []byte
				for pb.Next() {
					dst = tpl.renderStatic(dst)
				}
				sinkBytes = dst
			})
			b.StopTimer()
			reportPages(b, float64(b.Elapsed().Nanoseconds())/float64(b.N))
		})
	}
}

func BenchmarkRealSizeStaticSerial(b *testing.B) {
	for _, size := range realPageSizes {
		b.Run(strconv.Itoa(size)+"B", func(b *testing.B) {
			e := newTestEngine(b)
			tpl := mustCompile(b, e, buildSource(0, size, "k"))
			dst := tpl.renderStatic(nil)
			b.ReportAllocs()
			b.SetBytes(int64(len(dst)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				dst = tpl.renderStatic(dst)
			}
			b.StopTimer()
			sinkBytes = dst
			reportPages(b, float64(b.Elapsed().Nanoseconds())/float64(b.N))
		})
	}
}
