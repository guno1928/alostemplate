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

func (tpl *Template) renderLinear(base unsafe.Pointer, pos uintptr, end uintptr, values unsafe.Pointer, stride uintptr) uintptr {
	return tpl.writeLinear(base, pos, end, values, stride)
}

func (tpl *Template) renderFitting(dst []byte, values unsafe.Pointer, stride uintptr) uintptr {
	return tpl.writeFitting(dst, values, stride)
}
