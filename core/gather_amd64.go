//go:build amd64 && !purego

package core

import "unsafe"

const gatherAsmAvailable = true

//go:noescape
func gatherAsm(dst unsafe.Pointer, segs unsafe.Pointer, n int)
