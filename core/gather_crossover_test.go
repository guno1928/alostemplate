package core

import (
	"strconv"
	"testing"
	"unsafe"
)

var gatherSegSizes = []int{256, 512, 1024, 2048, 4096, 8192, 16384, 32768, 65536, 131072}

const gatherSegCount = 33

func gatherFixture(segSize int) ([]gatherSeg, []byte, int) {
	src := make([]byte, segSize)
	for i := range src {
		src[i] = byte('a' + i%26)
	}
	segs := make([]gatherSeg, gatherSegCount)
	for i := range segs {
		segs[i] = gatherSeg{ptr: unsafe.Pointer(unsafe.SliceData(src)), n: uintptr(segSize)}
	}
	total := segSize * gatherSegCount
	return segs, make([]byte, total), total
}

func BenchmarkGatherX_Asm(b *testing.B) {
	for _, size := range gatherSegSizes {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			segs, dst, total := gatherFixture(size)
			b.ReportAllocs()
			b.SetBytes(int64(total))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				gatherAsm(unsafe.Pointer(unsafe.SliceData(dst)), unsafe.Pointer(unsafe.SliceData(segs)), len(segs))
			}
			sinkBytes = dst
		})
	}
}

func BenchmarkGatherX_Go(b *testing.B) {
	for _, size := range gatherSegSizes {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			segs, dst, total := gatherFixture(size)
			b.ReportAllocs()
			b.SetBytes(int64(total))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				gatherGo(unsafe.Pointer(unsafe.SliceData(dst)), unsafe.Pointer(unsafe.SliceData(segs)), len(segs))
			}
			sinkBytes = dst
		})
	}
}

// BenchmarkGatherX_SingleMemmove is the same total bytes moved in one call,
// i.e. the floor if per-segment overhead were zero.
func BenchmarkGatherX_SingleMemmove(b *testing.B) {
	for _, size := range gatherSegSizes {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			total := size * gatherSegCount
			src := make([]byte, total)
			dst := make([]byte, total)
			b.ReportAllocs()
			b.SetBytes(int64(total))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				runtimeMemmove(unsafe.Pointer(unsafe.SliceData(dst)), unsafe.Pointer(unsafe.SliceData(src)), uintptr(total))
			}
			sinkBytes = dst
		})
	}
}

func TestGatherCrossoverAsmMatchesGo(t *testing.T) {
	for _, size := range gatherSegSizes {
		segs, dstAsm, total := gatherFixture(size)
		dstGo := make([]byte, total)
		gatherAsm(unsafe.Pointer(unsafe.SliceData(dstAsm)), unsafe.Pointer(unsafe.SliceData(segs)), len(segs))
		gatherGo(unsafe.Pointer(unsafe.SliceData(dstGo)), unsafe.Pointer(unsafe.SliceData(segs)), len(segs))
		if string(dstAsm) != string(dstGo) {
			t.Fatalf("segSize=%d: asm and go gather disagree", size)
		}
	}
}
