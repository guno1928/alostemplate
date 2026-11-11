package core

import "unsafe"

// gatherSeg is one {pointer, length} run of bytes to concatenate. The layout is
// mirrored by the assembly implementation in gather_amd64.s and must stay at
// two words.
type gatherSeg struct {
	ptr unsafe.Pointer
	n   uintptr
}

// gatherGo concatenates n segments into dst using the runtime memmove.
func gatherGo(dst unsafe.Pointer, segs unsafe.Pointer, n int) {
	pos := uintptr(0)
	for i := 0; i < n; i++ {
		seg := (*gatherSeg)(unsafe.Add(segs, uintptr(i)*unsafe.Sizeof(gatherSeg{})))
		if seg.n != 0 {
			runtimeMemmove(unsafe.Add(dst, pos), seg.ptr, seg.n)
		}
		pos += seg.n
	}
}

func gatherStreamsGo(dst unsafe.Pointer, room uintptr, first unsafe.Pointer, firstStride uintptr, second unsafe.Pointer, secondStride uintptr, n int) uintptr {
	pos := uintptr(0)
	firstOffset, secondOffset := uintptr(0), uintptr(0)
	for i := 0; i < n; i++ {
		seg := (*gatherSeg)(unsafe.Add(first, firstOffset))
		if seg.n > room-pos {
			break
		}
		if seg.n != 0 {
			runtimeMemmove(unsafe.Add(dst, pos), seg.ptr, seg.n)
		}
		pos += seg.n
		firstOffset += firstStride
		first, second = second, first
		firstOffset, secondOffset = secondOffset, firstOffset
		firstStride, secondStride = secondStride, firstStride
	}
	return pos
}
