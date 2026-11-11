//go:build amd64 && !purego

package core

import (
	"unsafe"

	"golang.org/x/sys/cpu"
)

const gatherAsmAvailable = true

var gatherUseAVX2 = cpu.X86.HasAVX2

//go:noescape
func gatherAsm(dst unsafe.Pointer, segs unsafe.Pointer, n int)

//go:noescape
func gatherStreamsAsm(dst unsafe.Pointer, room uintptr, first unsafe.Pointer, firstStride uintptr, second unsafe.Pointer, secondStride uintptr, n int) uintptr
