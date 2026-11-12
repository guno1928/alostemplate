package core

import (
	"crypto/rand"
	"encoding/binary"
	"math/bits"
	"unsafe"
)

const (
	inlineHashLimit     = 64
	hashBlock           = 16
	lengthSecretEntries = 128
	pointerIs64Bit      = unsafe.Sizeof(uintptr(0)) == 8
)

var (
	hashSecretA    = randomSecret()
	hashSecretB    = randomSecret()
	pairsHashSeed  = randomSecret()
	valuesHashSeed = randomSecret()
	mapHashSeed    = randomSecret()
	lengthSecrets  = randomLengthSecrets()
)

func randomSecret() uint64 {
	var raw [8]byte
	rand.Read(raw[:])
	return binary.LittleEndian.Uint64(raw[:])
}

func randomLengthSecrets() (secrets [lengthSecretEntries]uint64) {
	for i := range secrets {
		secrets[i] = randomSecret()
	}
	return secrets
}

func loadLE64(p unsafe.Pointer) uint64 {
	return binary.LittleEndian.Uint64((*[8]byte)(p)[:])
}

func loadLE32(p unsafe.Pointer) uint64 {
	return uint64(binary.LittleEndian.Uint32((*[4]byte)(p)[:]))
}

func foldMultiply(a uint64, b uint64) uint64 {
	hi, lo := bits.Mul64(a, b)
	return hi ^ lo
}

func hashLongBytes(p unsafe.Pointer, h uint64, n int) uint64 {
	if pointerIs64Bit {
		return uint64(runtimeMemhash(p, uintptr(h), uintptr(n)))
	}
	low := runtimeMemhash(p, uintptr(h), uintptr(n))
	high := runtimeMemhash(p, uintptr(h>>32)^low, uintptr(n))
	return uint64(high)<<32 | uint64(low)
}

func hashStrings(seed uint64, items []string) uint64 {
	h := seed ^ uint64(len(items))*hashSecretA
	for _, s := range items {
		n := len(s)
		p := unsafe.Pointer(unsafe.StringData(s))
		var a, b uint64
		if n <= hashBlock {
			if n >= 8 {
				a = loadLE64(p)
				b = loadLE64(unsafe.Add(p, n-8))
			} else if n >= 4 {
				a = loadLE32(p)
				b = loadLE32(unsafe.Add(p, n-4))
			} else if n > 0 {
				a = uint64(*(*byte)(p))<<16 | uint64(*(*byte)(unsafe.Add(p, n>>1)))<<8 | uint64(*(*byte)(unsafe.Add(p, n-1)))
			}
		} else if n <= 2*hashBlock {
			a = loadLE64(unsafe.Add(p, n-16)) ^ foldMultiply(loadLE64(p)^hashSecretA, loadLE64(unsafe.Add(p, 8))^hashSecretB)
			b = loadLE64(unsafe.Add(p, n-8))
		} else if n <= inlineHashLimit {
			blocks := foldMultiply(loadLE64(p)^hashSecretA, loadLE64(unsafe.Add(p, 8))^hashSecretB)
			for offset := hashBlock; offset < n-hashBlock; offset += hashBlock {
				blocks = foldMultiply(loadLE64(unsafe.Add(p, offset))^hashSecretA, loadLE64(unsafe.Add(p, offset+8))^blocks^hashSecretB)
			}
			a = loadLE64(unsafe.Add(p, n-16)) ^ blocks
			b = loadLE64(unsafe.Add(p, n-8))
		} else {
			a = hashLongBytes(p, hashSecretB, n)
			b = uint64(n)
		}
		h = foldMultiply(a^hashSecretA, b^h^lengthSecrets[n&(lengthSecretEntries-1)])
	}
	return h
}
