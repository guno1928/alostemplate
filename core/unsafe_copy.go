package core

import "unsafe"

//go:linkname runtimeMemmove runtime.memmove
//go:noescape
func runtimeMemmove(to unsafe.Pointer, from unsafe.Pointer, n uintptr)

//go:linkname runtimeMallocgc runtime.mallocgc
func runtimeMallocgc(size uintptr, typ unsafe.Pointer, needzero bool) unsafe.Pointer

//go:linkname runtimeMemhash runtime.memhash
//go:noescape
func runtimeMemhash(p unsafe.Pointer, seed uintptr, size uintptr) uintptr

//go:noinline
func makeUninitializedBytes(n int) []byte {
	if n <= 0 {
		return nil
	}
	return unsafe.Slice((*byte)(runtimeMallocgc(uintptr(n), nil, false)), n)
}
