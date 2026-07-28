package core

import (
	"testing"
)

// The round-two benchmarks fed pairs in template order, which is exactly the
// fast path alternative 9 is built around. These benchmarks re-measure the same
// alternatives against deterministically shuffled pairs so the worst case is on
// the record next to the best case.

func shuffledPairs(n int) []string {
	pairs := buildPairs(n, "key", 12)
	out := make([]string, len(pairs))
	copy(out, pairs)
	// Deterministic Fisher-Yates over pair slots using a fixed LCG so the
	// benchmark is reproducible without depending on map iteration order.
	state := uint64(0x2545F4914F6CDD1D)
	for i := n - 1; i > 0; i-- {
		state = state*6364136223846793005 + 1442695040888963407
		j := int((state >> 33) % uint64(i+1))
		out[i*2], out[j*2] = out[j*2], out[i*2]
		out[i*2+1], out[j*2+1] = out[j*2+1], out[i*2+1]
	}
	return out
}

func TestShuffledPairsStillResolve(t *testing.T) {
	for _, n := range []int{4, 8, 14, 16, 64} {
		keys := altKeysFor(n)
		meta := altBuildMeta(keys)
		pairs := shuffledPairs(n)

		wantResolved := make([]string, n)
		wantFound := make([]bool, n)
		altResolveOriginal(keys, pairs, wantResolved, wantFound)

		gotResolved := make([]string, n)
		gotFound := make([]bool, n)
		altResolvePositional(keys, meta, pairs, gotResolved, gotFound)

		for i := 0; i < n; i++ {
			if gotFound[i] != wantFound[i] || gotResolved[i] != wantResolved[i] {
				t.Fatalf("n=%d idx=%d got (%q,%v) want (%q,%v)", n, i, gotResolved[i], gotFound[i], wantResolved[i], wantFound[i])
			}
		}
	}
}

func benchResolveShuffled(b *testing.B, n int) {
	keys := altKeysFor(n)
	pairs := shuffledPairs(n)
	meta := altBuildMeta(keys)
	table := altBuildKeyTable(keys)
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
}

func BenchmarkAltResolveShuffled4(b *testing.B)  { benchResolveShuffled(b, 4) }
func BenchmarkAltResolveShuffled8(b *testing.B)  { benchResolveShuffled(b, 8) }
func BenchmarkAltResolveShuffled14(b *testing.B) { benchResolveShuffled(b, 14) }
func BenchmarkAltResolveShuffled16(b *testing.B) { benchResolveShuffled(b, 16) }
func BenchmarkAltResolveShuffled64(b *testing.B) { benchResolveShuffled(b, 64) }
