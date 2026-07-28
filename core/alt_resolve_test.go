package core

import (
	"strconv"
	"testing"
	"unsafe"
)

// Alternative implementations of the key-resolution step, which the CPU profile
// identified as 83% of BenchmarkReplacePairsWarm/slots64 and 45% of
// BenchmarkReplaceRealisticHTMLPairs. Each alternative fills resolved/found for
// every template key from a flat []string of key/value pairs, preserving the
// first-occurrence-wins semantics of findReplacement.

// Alternative 1: the current production implementation. For each template key,
// linear-scan the whole pairs slice. O(keys x pairs).
func altResolveOriginal(keys []string, pairs []string, resolved []string, found []bool) {
	for i, key := range keys {
		value, ok := findReplacement(pairs, key)
		if ok {
			resolved[i] = value
			found[i] = true
		}
	}
}

// Alternative 2: invert the loops. Walk pairs once and linear-scan keys, using a
// length prefilter before the full string compare. Still O(keys x pairs) but with
// a much cheaper inner comparison.
func altResolveInverted(keys []string, pairs []string, resolved []string, found []bool) {
	for i := 0; i+1 < len(pairs); i += 2 {
		pk := pairs[i]
		for j, key := range keys {
			if len(key) != len(pk) || found[j] {
				continue
			}
			if key == pk {
				resolved[j] = pairs[i+1]
				found[j] = true
				break
			}
		}
	}
}

// Alternative 3: keep the original loop order but prefilter each candidate on
// length and first byte before paying for a full memequal.
func altResolveLenFirstByte(keys []string, pairs []string, resolved []string, found []bool) {
	for i, key := range keys {
		n := len(key)
		if n == 0 {
			continue
		}
		c := key[0]
		for j := 0; j+1 < len(pairs); j += 2 {
			pk := pairs[j]
			if len(pk) != n || pk[0] != c {
				continue
			}
			if pk == key {
				resolved[i] = pairs[j+1]
				found[i] = true
				break
			}
		}
	}
}

// Alternative 4: a Go map built at compile time, probed once per pair.
func altResolveStdMap(index map[string]int32, pairs []string, resolved []string, found []bool) {
	for i := 0; i+1 < len(pairs); i += 2 {
		if idx, ok := index[pairs[i]]; ok && !found[idx] {
			resolved[idx] = pairs[i+1]
			found[idx] = true
		}
	}
}

// Alternative 5: an open-addressed hash table built at compile time, probed once
// per pair. O(pairs) with no per-key rescan.
type altKHEntry struct {
	hash uint64
	idx  int32
	used int32
}

type altKeyTable struct {
	mask    uint64
	entries []altKHEntry
	keys    []string
}

func altBuildKeyTable(keys []string) *altKeyTable {
	size := uint64(8)
	for size < uint64(len(keys))*2 {
		size <<= 1
	}
	t := &altKeyTable{mask: size - 1, entries: make([]altKHEntry, size), keys: keys}
	for i, k := range keys {
		h := altHashString(k)
		pos := h & t.mask
		for t.entries[pos].used != 0 {
			pos = (pos + 1) & t.mask
		}
		t.entries[pos] = altKHEntry{hash: h, idx: int32(i), used: 1}
	}
	return t
}

func altHashString(s string) uint64 {
	return hashPlaceholderKey(s)
}

func altResolveHashTable(t *altKeyTable, pairs []string, resolved []string, found []bool) {
	mask := t.mask
	entries := t.entries
	for i := 0; i+1 < len(pairs); i += 2 {
		pk := pairs[i]
		h := altHashString(pk)
		pos := h & mask
		for {
			e := &entries[pos]
			if e.used == 0 {
				break
			}
			if e.hash == h && t.keys[e.idx] == pk {
				if !found[e.idx] {
					resolved[e.idx] = pairs[i+1]
					found[e.idx] = true
				}
				break
			}
			pos = (pos + 1) & mask
		}
	}
}

// Alternative 6: hash table plus an unsafe 8-byte prefix compare that rejects
// non-matching keys without calling into memequal.
type altKHEntry2 struct {
	hash   uint64
	prefix uint64
	idx    int32
	length int32
	used   int32
	_      int32
}

type altKeyTable2 struct {
	mask    uint64
	entries []altKHEntry2
	keys    []string
}

func altPrefix8(s string) uint64 {
	n := len(s)
	if n >= 8 {
		return *(*uint64)(unsafe.Pointer(unsafe.StringData(s)))
	}
	var v uint64
	p := unsafe.Pointer(unsafe.StringData(s))
	for i := 0; i < n; i++ {
		v |= uint64(*(*byte)(unsafe.Add(p, i))) << (8 * uint(i))
	}
	return v
}

func altBuildKeyTable2(keys []string) *altKeyTable2 {
	size := uint64(8)
	for size < uint64(len(keys))*2 {
		size <<= 1
	}
	t := &altKeyTable2{mask: size - 1, entries: make([]altKHEntry2, size), keys: keys}
	for i, k := range keys {
		h := altHashString(k)
		pos := h & t.mask
		for t.entries[pos].used != 0 {
			pos = (pos + 1) & t.mask
		}
		t.entries[pos] = altKHEntry2{
			hash:   h,
			prefix: altPrefix8(k),
			idx:    int32(i),
			length: int32(len(k)),
			used:   1,
		}
	}
	return t
}

func altResolveHashTableUnsafe(t *altKeyTable2, pairs []string, resolved []string, found []bool) {
	mask := t.mask
	entries := t.entries
	for i := 0; i+1 < len(pairs); i += 2 {
		pk := pairs[i]
		h := altHashString(pk)
		pfx := altPrefix8(pk)
		ln := int32(len(pk))
		pos := h & mask
		for {
			e := &entries[pos]
			if e.used == 0 {
				break
			}
			if e.hash == h && e.length == ln && e.prefix == pfx {
				if ln <= 8 || t.keys[e.idx] == pk {
					if !found[e.idx] {
						resolved[e.idx] = pairs[i+1]
						found[e.idx] = true
					}
				}
				break
			}
			pos = (pos + 1) & mask
		}
	}
}

func altKeysFor(n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = "key" + strconv.Itoa(i)
	}
	return keys
}

func altStdIndex(keys []string) map[string]int32 {
	m := make(map[string]int32, len(keys))
	for i, k := range keys {
		m[k] = int32(i)
	}
	return m
}

// TestAltResolveEquivalence proves every alternative produces byte-identical
// output to the current production implementation across sizes, missing keys,
// extra keys, and duplicate pairs.
func TestAltResolveEquivalence(t *testing.T) {
	for _, n := range []int{1, 2, 4, 8, 16, 33, 64, 128} {
		keys := altKeysFor(n)
		table := altBuildKeyTable(keys)
		table2 := altBuildKeyTable2(keys)
		index := altStdIndex(keys)

		variants := map[string][]string{
			"full":       buildPairs(n, "key", 9),
			"empty":      nil,
			"partial":    buildPairs(n/2+1, "key", 9),
			"extra":      append(buildPairs(n, "key", 9), "unrelated", "zzz", "another", "yyy"),
			"duplicates": append(buildPairs(n, "key", 9), "key0", "SECOND"),
			"oddtail":    append(buildPairs(n, "key", 9), "dangling"),
		}

		for name, pairs := range variants {
			wantResolved := make([]string, n)
			wantFound := make([]bool, n)
			altResolveOriginal(keys, pairs, wantResolved, wantFound)

			check := func(label string, fn func(r []string, f []bool)) {
				gotResolved := make([]string, n)
				gotFound := make([]bool, n)
				fn(gotResolved, gotFound)
				for i := 0; i < n; i++ {
					if gotFound[i] != wantFound[i] || gotResolved[i] != wantResolved[i] {
						t.Fatalf("n=%d variant=%s alt=%s idx=%d got (%q,%v) want (%q,%v)",
							n, name, label, i, gotResolved[i], gotFound[i], wantResolved[i], wantFound[i])
					}
				}
			}

			check("inverted", func(r []string, f []bool) { altResolveInverted(keys, pairs, r, f) })
			check("lenfirstbyte", func(r []string, f []bool) { altResolveLenFirstByte(keys, pairs, r, f) })
			check("stdmap", func(r []string, f []bool) { altResolveStdMap(index, pairs, r, f) })
			check("hashtable", func(r []string, f []bool) { altResolveHashTable(table, pairs, r, f) })
			check("hashunsafe", func(r []string, f []bool) { altResolveHashTableUnsafe(table2, pairs, r, f) })
		}
	}
}

func TestAltHashStringDistinct(t *testing.T) {
	seen := make(map[uint64]string)
	keys := append(altKeysFor(512), "", "a", "ab", "abcdefgh", "abcdefghi", "verylongkeyname_with_suffix")
	for _, k := range keys {
		h := altHashString(k)
		if prev, ok := seen[h]; ok && prev != k {
			t.Fatalf("hash collision between %q and %q", prev, k)
		}
		seen[h] = k
	}
}

func TestAltPrefix8(t *testing.T) {
	if altPrefix8("") != 0 {
		t.Fatal("empty prefix should be 0")
	}
	if altPrefix8("a") != uint64('a') {
		t.Fatal("single byte prefix wrong")
	}
	if altPrefix8("ab") != uint64('a')|uint64('b')<<8 {
		t.Fatal("two byte prefix wrong")
	}
	long := altPrefix8("abcdefghXXXX")
	if long != altPrefix8("abcdefgh") {
		t.Fatal("prefix should only read first 8 bytes")
	}
}

func benchResolve(b *testing.B, n int) {
	keys := altKeysFor(n)
	pairs := buildPairs(n, "key", 12)
	table := altBuildKeyTable(keys)
	table2 := altBuildKeyTable2(keys)
	index := altStdIndex(keys)
	resolved := make([]string, n)
	found := make([]bool, n)

	reset := func() {
		clear(resolved)
		clear(found)
	}

	b.Run("1_original", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			reset()
			altResolveOriginal(keys, pairs, resolved, found)
		}
	})
	b.Run("2_inverted", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			reset()
			altResolveInverted(keys, pairs, resolved, found)
		}
	})
	b.Run("3_lenfirstbyte", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			reset()
			altResolveLenFirstByte(keys, pairs, resolved, found)
		}
	})
	b.Run("4_stdmap", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			reset()
			altResolveStdMap(index, pairs, resolved, found)
		}
	})
	b.Run("5_hashtable", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			reset()
			altResolveHashTable(table, pairs, resolved, found)
		}
	})
	b.Run("6_hashunsafe", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			reset()
			altResolveHashTableUnsafe(table2, pairs, resolved, found)
		}
	})
}

func BenchmarkAltResolve1(b *testing.B)  { benchResolve(b, 1) }
func BenchmarkAltResolve4(b *testing.B)  { benchResolve(b, 4) }
func BenchmarkAltResolve8(b *testing.B)  { benchResolve(b, 8) }
func BenchmarkAltResolve14(b *testing.B) { benchResolve(b, 14) }
func BenchmarkAltResolve16(b *testing.B) { benchResolve(b, 16) }
func BenchmarkAltResolve64(b *testing.B) { benchResolve(b, 64) }
