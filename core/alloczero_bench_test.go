package core

import (
	"strconv"
	"testing"
	"unsafe"
)

var allocSizes = []int{1024, 9472, 32768, 65536, 150000}

// BenchmarkAllocMakeLen is what gatherParts does today: make([]byte, total),
// which the spec requires to be zero-initialised.
func BenchmarkAllocMakeLen(b *testing.B) {
	for _, size := range allocSizes {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sinkBytes = make([]byte, size)
			}
		})
	}
}

// BenchmarkAllocMakeCap allocates the same capacity with zero length, to test
// whether the runtime skips the clear when nothing is readable yet.
func BenchmarkAllocMakeCap(b *testing.B) {
	for _, size := range allocSizes {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				buf := make([]byte, 0, size)
				sinkBytes = buf[:size]
			}
		})
	}
}

// BenchmarkAllocMakeCapUnsafe extends the zero-length allocation to full length
// through unsafe.Slice rather than reslicing, in case reslicing re-clears.
func BenchmarkAllocMakeCapUnsafe(b *testing.B) {
	for _, size := range allocSizes {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				buf := make([]byte, 0, size)
				sinkBytes = unsafe.Slice(unsafe.SliceData(buf), size)
			}
		})
	}
}

// BenchmarkAllocThenFill is the realistic comparison: allocate and then write
// every byte, which is what a render actually does.
func BenchmarkAllocThenFillMakeLen(b *testing.B) {
	for _, size := range allocSizes {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			src := make([]byte, size)
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				dst := make([]byte, size)
				runtimeMemmove(unsafe.Pointer(unsafe.SliceData(dst)), unsafe.Pointer(unsafe.SliceData(src)), uintptr(size))
				sinkBytes = dst
			}
		})
	}
}

func BenchmarkAllocThenFillMakeCap(b *testing.B) {
	for _, size := range allocSizes {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			src := make([]byte, size)
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				buf := make([]byte, 0, size)
				dst := buf[:size]
				runtimeMemmove(unsafe.Pointer(unsafe.SliceData(dst)), unsafe.Pointer(unsafe.SliceData(src)), uintptr(size))
				sinkBytes = dst
			}
		})
	}
}
