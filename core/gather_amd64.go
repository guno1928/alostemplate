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
func renderLinearAsm(base unsafe.Pointer, pos uintptr, end uintptr, tpl *Template, values unsafe.Pointer, stride uintptr) uintptr

//go:noescape
func renderFittingAsm(base unsafe.Pointer, pos uintptr, limit uintptr, tpl *Template, values unsafe.Pointer, stride uintptr) uintptr

func (tpl *Template) renderLinear(base unsafe.Pointer, pos uintptr, end uintptr, values unsafe.Pointer, stride uintptr) uintptr {
	return renderLinearAsm(base, pos, end, tpl, values, stride)
}

func (tpl *Template) renderFitting(dst []byte, values unsafe.Pointer, stride uintptr) uintptr {
	return renderFittingAsm(unsafe.Pointer(unsafe.SliceData(dst)), uintptr(len(dst)), uintptr(cap(dst)), tpl, values, stride)
}
