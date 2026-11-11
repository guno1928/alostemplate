//go:build !amd64 || purego

package core

import "unsafe"

const (
	gatherAsmAvailable = false
	gatherUseAVX2      = false
)

func gatherAsm(dst unsafe.Pointer, segs unsafe.Pointer, n int) {
	gatherGo(dst, segs, n)
}

func gatherStreamsAsm(dst unsafe.Pointer, room uintptr, first unsafe.Pointer, firstStride uintptr, second unsafe.Pointer, secondStride uintptr, n int) uintptr {
	return gatherStreamsGo(dst, room, first, firstStride, second, secondStride, n)
}
