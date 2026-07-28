package core

import "testing"

func (tpl *Template) emitPartsOld(parts []string, resolved []string, found []bool) ([]string, int) {
	total := tpl.staticLen
	literals := tpl.literals
	for i, slot := range tpl.slots {
		var value string
		if found[slot.keyIndex] {
			value = resolved[slot.keyIndex]
		} else {
			value = slot.placeholder
		}
		total += len(value)
		parts = append(parts, literals[i], value)
	}
	parts = append(parts, literals[len(literals)-1])
	return parts, total
}

func (tpl *Template) resolvePairsOld(pairs []string, resolved []string, found []bool) {
	keys := tpl.keys
	n := len(keys)

	if len(pairs) == n*2 {
		i := 0
		for ; i < n; i++ {
			if pairs[i*2] != keys[i] {
				break
			}
		}
		if i == n {
			for j := 0; j < n; j++ {
				resolved[j] = pairs[j*2+1]
				found[j] = true
			}
			return
		}
	}

	mask := tpl.table.mask
	entries := tpl.table.entries
	for i := 0; i+1 < len(pairs); i += 2 {
		pk := pairs[i]
		h := hashPlaceholderKey(pk)
		pos := h & mask
		for {
			e := &entries[pos]
			if e.used == 0 {
				break
			}
			if e.hash == h && keys[e.idx] == pk {
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

func bceFixture(b *testing.B) (*Template, []string, []string, []bool, []string) {
	b.Helper()
	e := newTestEngine(b)
	tpl := mustCompile(b, e, buildSource(bmSlots, bmLiteralLen, "k"))
	pairs := buildPairs(bmSlots, "k", 24)
	resolved := make([]string, len(tpl.keys))
	found := make([]bool, len(tpl.keys))
	tpl.resolvePairs(pairs, resolved, found)
	parts := make([]string, 0, 2*len(tpl.slots)+1)
	return tpl, pairs, resolved, found, parts
}

func BenchmarkBCE_emitPartsOld(b *testing.B) {
	tpl, _, resolved, found, parts := bceFixture(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkParts, sinkInt = tpl.emitPartsOld(parts[:0], resolved, found)
	}
}

func BenchmarkBCE_emitPartsNew(b *testing.B) {
	tpl, _, resolved, found, parts := bceFixture(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkParts, sinkInt = tpl.emitParts(parts[:0], resolved, found)
	}
}

func BenchmarkBCE_resolvePairsOld(b *testing.B) {
	tpl, pairs, resolved, found, _ := bceFixture(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		clear(found)
		tpl.resolvePairsOld(pairs, resolved, found)
	}
	sinkStrings = resolved
}

func BenchmarkBCE_resolvePairsNew(b *testing.B) {
	tpl, pairs, resolved, found, _ := bceFixture(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		clear(found)
		tpl.resolvePairs(pairs, resolved, found)
	}
	sinkStrings = resolved
}

func TestBCEVariantsAgree(t *testing.T) {
	e := newTestEngine(t)
	for _, slots := range []int{2, 4, 8, 16, 64} {
		tpl := mustCompile(t, e, buildSource(slots, 64, "k"))
		pairs := buildPairs(slots, "k", 24)

		wantResolved := make([]string, len(tpl.keys))
		wantFound := make([]bool, len(tpl.keys))
		tpl.resolvePairsOld(pairs, wantResolved, wantFound)

		gotResolved := make([]string, len(tpl.keys))
		gotFound := make([]bool, len(tpl.keys))
		tpl.resolvePairs(pairs, gotResolved, gotFound)

		for i := range wantResolved {
			if gotResolved[i] != wantResolved[i] || gotFound[i] != wantFound[i] {
				t.Fatalf("slots=%d idx=%d resolvePairs mismatch", slots, i)
			}
		}

		wantParts, wantTotal := tpl.emitPartsOld(nil, wantResolved, wantFound)
		gotParts, gotTotal := tpl.emitParts(nil, gotResolved, gotFound)
		if gotTotal != wantTotal || len(gotParts) != len(wantParts) {
			t.Fatalf("slots=%d emitParts totals differ: %d/%d parts %d/%d",
				slots, gotTotal, wantTotal, len(gotParts), len(wantParts))
		}
		for i := range wantParts {
			if gotParts[i] != wantParts[i] {
				t.Fatalf("slots=%d part %d differs", slots, i)
			}
		}
	}
}
