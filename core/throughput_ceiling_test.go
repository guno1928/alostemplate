package core

import (
	"strconv"
	"testing"
	"unsafe"
)

// pagesPerSecond converts ns/op into pages/sec so the 150M/s target can be read
// directly off each benchmark line.
func reportPages(b *testing.B, nsPerOp float64) {
	if nsPerOp > 0 {
		b.ReportMetric(1e9/nsPerOp/1e6, "Mpages/s")
	}
}

var ceilingSizes = []int{64, 128, 256, 512, 1024, 4096, 16384, 65536}

// BenchmarkCeilingSingleSlotReused is the fastest render path that exists:
// one placeholder, prefix/suffix memmove, reused output buffer, no allocation.
func BenchmarkCeilingSingleSlotReused(b *testing.B) {
	for _, size := range ceilingSizes {
		b.Run(strconv.Itoa(size)+"B", func(b *testing.B) {
			e := newTestEngine(b)
			src, value := singleSlotPage(size, 16)
			tpl := mustCompile(b, e, src)
			pairs := []string{value}
			dst := tpl.replaceSingle(nil, pairs)
			b.ReportAllocs()
			b.SetBytes(int64(len(dst)))
			b.ResetTimer()
			start := b.N
			for i := 0; i < b.N; i++ {
				dst = tpl.replaceSingle(dst, pairs)
			}
			b.StopTimer()
			_ = start
			sinkBytes = dst
			reportPages(b, float64(b.Elapsed().Nanoseconds())/float64(b.N))
		})
	}
}

// BenchmarkCeilingSingleSlotParallel is the same path across all cores.
func BenchmarkCeilingSingleSlotParallel(b *testing.B) {
	for _, size := range ceilingSizes {
		b.Run(strconv.Itoa(size)+"B", func(b *testing.B) {
			e := newTestEngine(b)
			src, value := singleSlotPage(size, 16)
			tpl := mustCompile(b, e, src)
			pairs := []string{value}
			warm := tpl.replaceSingle(nil, pairs)
			b.ReportAllocs()
			b.SetBytes(int64(len(warm)))
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				var dst []byte
				for pb.Next() {
					dst = tpl.replaceSingle(dst, pairs)
				}
				sinkBytes = dst
			})
			b.StopTimer()
			reportPages(b, float64(b.Elapsed().Nanoseconds())/float64(b.N))
		})
	}
}

// BenchmarkCeilingStaticReused renders a template with no placeholders at all,
// which is a pure memmove of the whole page and therefore the absolute upper
// bound for any templating engine that must produce the bytes.
func BenchmarkCeilingStaticReused(b *testing.B) {
	for _, size := range ceilingSizes {
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

// BenchmarkCeilingRawMemmove removes the templating entirely and measures only
// the cost of moving the page-sized bytes. Nothing that produces a page can
// beat this number.
func BenchmarkCeilingRawMemmove(b *testing.B) {
	for _, size := range ceilingSizes {
		b.Run(strconv.Itoa(size)+"B", func(b *testing.B) {
			src := make([]byte, size)
			dst := make([]byte, size)
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				runtimeMemmove(unsafe.Pointer(unsafe.SliceData(dst)), unsafe.Pointer(unsafe.SliceData(src)), uintptr(size))
			}
			b.StopTimer()
			sinkBytes = dst
			reportPages(b, float64(b.Elapsed().Nanoseconds())/float64(b.N))
		})
	}
}

// BenchmarkCeilingRawMemmoveParallel is the same bound across all cores, i.e.
// the machine's achievable aggregate memory bandwidth for this access pattern.
func BenchmarkCeilingRawMemmoveParallel(b *testing.B) {
	for _, size := range ceilingSizes {
		b.Run(strconv.Itoa(size)+"B", func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				src := make([]byte, size)
				dst := make([]byte, size)
				for pb.Next() {
					runtimeMemmove(unsafe.Pointer(unsafe.SliceData(dst)), unsafe.Pointer(unsafe.SliceData(src)), uintptr(size))
				}
				sinkBytes = dst
			})
			b.StopTimer()
			reportPages(b, float64(b.Elapsed().Nanoseconds())/float64(b.N))
		})
	}
}

// BenchmarkCeilingNoOpFunctionCall establishes the floor: the cost of calling
// through a function boundary and returning, doing no work at all.
func BenchmarkCeilingNoOpFunctionCall(b *testing.B) {
	e := newTestEngine(b)
	tpl := mustCompile(b, e, "x")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkTpl = tpl.renderTarget()
	}
	b.StopTimer()
	reportPages(b, float64(b.Elapsed().Nanoseconds())/float64(b.N))
}
