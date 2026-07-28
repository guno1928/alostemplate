package core

import (
	"testing"
	"unsafe"
)

// Round two of key-resolution alternatives, focused on the 4-16 key range that
// real templates occupy. Round one showed the inverted loop wins there, so these
// variants attack the cost of its inner comparison.

type altKeyMeta struct {
	prefix uint64
	length int32
	_      int32
}

func altBuildMeta(keys []string) []altKeyMeta {
	meta := make([]altKeyMeta, len(keys))
	for i, k := range keys {
		meta[i] = altKeyMeta{prefix: altPrefix8(k), length: int32(len(k))}
	}
	return meta
}

// Alternative 7: inverted loop where the inner comparison is two integer
// compares (length + first 8 bytes) instead of a string compare. Keys of 8 bytes
// or fewer are fully determined by that pair, so no memequal is needed at all.
func altResolvePrefixMeta(keys []string, meta []altKeyMeta, pairs []string, resolved []string, found []bool) {
	for i := 0; i+1 < len(pairs); i += 2 {
		pk := pairs[i]
		ln := int32(len(pk))
		pfx := altPrefix8(pk)
		for j := range meta {
			m := &meta[j]
			if m.length != ln || m.prefix != pfx || found[j] {
				continue
			}
			if ln <= 8 || keys[j] == pk {
				resolved[j] = pairs[i+1]
				found[j] = true
				break
			}
		}
	}
}

// Alternative 8: prefix-meta comparison plus a rotating start offset. Callers
// commonly build pairs in template order, so starting the scan at the pair's own
// index makes that case resolve on the first probe instead of scanning.
func altResolveRotating(keys []string, meta []altKeyMeta, pairs []string, resolved []string, found []bool) {
	n := len(meta)
	if n == 0 {
		return
	}
	start := 0
	for i := 0; i+1 < len(pairs); i += 2 {
		pk := pairs[i]
		ln := int32(len(pk))
		pfx := altPrefix8(pk)
		j := start
		for c := 0; c < n; c++ {
			m := &meta[j]
			if m.length == ln && m.prefix == pfx && !found[j] {
				if ln <= 8 || keys[j] == pk {
					resolved[j] = pairs[i+1]
					found[j] = true
					start = j + 1
					if start >= n {
						start = 0
					}
					break
				}
			}
			j++
			if j >= n {
				j = 0
			}
		}
	}
}

// Alternative 9: fully unrolled positional fast path. If the caller supplied the
// keys in exactly template order, fill everything with no searching at all and
// fall back to the rotating scan otherwise.
func altResolvePositional(keys []string, meta []altKeyMeta, pairs []string, resolved []string, found []bool) {
	n := len(keys)
	if len(pairs) == n*2 {
		ok := true
		for j := 0; j < n; j++ {
			if pairs[j*2] != keys[j] {
				ok = false
				break
			}
		}
		if ok {
			for j := 0; j < n; j++ {
				resolved[j] = pairs[j*2+1]
				found[j] = true
			}
			return
		}
		clear(resolved[:n])
		clear(found[:n])
	}
	altResolveRotating(keys, meta, pairs, resolved, found)
}

// Alternative 10: hash table with the rotating-scan structure replaced by a
// direct-mapped cache keyed on the low bits of the key length and first byte.
type altDirectTable struct {
	buckets [64]int32
	keys    []string
	meta    []altKeyMeta
}

func altBuildDirect(keys []string) *altDirectTable {
	t := &altDirectTable{keys: keys, meta: altBuildMeta(keys)}
	for i := range t.buckets {
		t.buckets[i] = -1
	}
	for i, k := range keys {
		if len(k) == 0 {
			continue
		}
		slot := (uint32(len(k))*31 ^ uint32(k[0])) & 63
		if t.buckets[slot] == -1 {
			t.buckets[slot] = int32(i)
		} else {
			t.buckets[slot] = -2
		}
	}
	return t
}

func altResolveDirect(t *altDirectTable, pairs []string, resolved []string, found []bool) {
	for i := 0; i+1 < len(pairs); i += 2 {
		pk := pairs[i]
		if len(pk) == 0 {
			continue
		}
		slot := (uint32(len(pk))*31 ^ uint32(pk[0])) & 63
		idx := t.buckets[slot]
		if idx == -1 {
			continue
		}
		if idx >= 0 {
			if !found[idx] && t.keys[idx] == pk {
				resolved[idx] = pairs[i+1]
				found[idx] = true
			}
			continue
		}
		ln := int32(len(pk))
		pfx := altPrefix8(pk)
		for j := range t.meta {
			m := &t.meta[j]
			if m.length != ln || m.prefix != pfx || found[j] {
				continue
			}
			if ln <= 8 || t.keys[j] == pk {
				resolved[j] = pairs[i+1]
				found[j] = true
				break
			}
		}
	}
}

var _ = unsafe.Pointer(nil)

func TestAltResolve2Equivalence(t *testing.T) {
	for _, n := range []int{1, 2, 4, 8, 14, 16, 33, 64, 128} {
		keys := altKeysFor(n)
		meta := altBuildMeta(keys)
		direct := altBuildDirect(keys)

		variants := map[string][]string{
			"full":       buildPairs(n, "key", 9),
			"empty":      nil,
			"partial":    buildPairs(n/2+1, "key", 9),
			"extra":      append(buildPairs(n, "key", 9), "unrelated", "zzz"),
			"duplicates": append(buildPairs(n, "key", 9), "key0", "SECOND"),
			"oddtail":    append(buildPairs(n, "key", 9), "dangling"),
			"reversed":   reversePairs(buildPairs(n, "key", 9)),
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

			check("prefixmeta", func(r []string, f []bool) { altResolvePrefixMeta(keys, meta, pairs, r, f) })
			check("rotating", func(r []string, f []bool) { altResolveRotating(keys, meta, pairs, r, f) })
			check("positional", func(r []string, f []bool) { altResolvePositional(keys, meta, pairs, r, f) })
			check("direct", func(r []string, f []bool) { altResolveDirect(direct, pairs, r, f) })
		}
	}
}

func reversePairs(pairs []string) []string {
	out := make([]string, 0, len(pairs))
	for i := len(pairs) - 2; i >= 0; i -= 2 {
		out = append(out, pairs[i], pairs[i+1])
	}
	return out
}

func benchResolve2(b *testing.B, n int) {
	keys := altKeysFor(n)
	pairs := buildPairs(n, "key", 12)
	meta := altBuildMeta(keys)
	table := altBuildKeyTable(keys)
	direct := altBuildDirect(keys)
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
	b.Run("5_hashtable", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			reset()
			altResolveHashTable(table, pairs, resolved, found)
		}
	})
	b.Run("7_prefixmeta", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			reset()
			altResolvePrefixMeta(keys, meta, pairs, resolved, found)
		}
	})
	b.Run("8_rotating", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			reset()
			altResolveRotating(keys, meta, pairs, resolved, found)
		}
	})
	b.Run("9_positional", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			reset()
			altResolvePositional(keys, meta, pairs, resolved, found)
		}
	})
	b.Run("10_direct", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			reset()
			altResolveDirect(direct, pairs, resolved, found)
		}
	})
}

func BenchmarkAltResolve2_4(b *testing.B)  { benchResolve2(b, 4) }
func BenchmarkAltResolve2_8(b *testing.B)  { benchResolve2(b, 8) }
func BenchmarkAltResolve2_14(b *testing.B) { benchResolve2(b, 14) }
func BenchmarkAltResolve2_16(b *testing.B) { benchResolve2(b, 16) }
func BenchmarkAltResolve2_64(b *testing.B) { benchResolve2(b, 64) }
