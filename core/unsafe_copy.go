package core

import "unsafe"

//go:linkname runtimeMemmove runtime.memmove
func runtimeMemmove(to unsafe.Pointer, from unsafe.Pointer, n uintptr)
