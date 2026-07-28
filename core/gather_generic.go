//go:build !amd64 || purego

package core

import "unsafe"

const gatherAsmAvailable = false

func gatherAsm(dst unsafe.Pointer, segs unsafe.Pointer, n int) {
	gatherGo(dst, segs, n)
}
