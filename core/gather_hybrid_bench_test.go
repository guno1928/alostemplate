package core

import (
	"strconv"
	"testing"
	"unsafe"
)

func gatherPartsForcedAsm(dst []byte, parts []string, total int) []byte {
	if cap(dst) < total {
		dst = make([]byte, total)
	} else {
		dst = dst[:total]
	}
	if total == 0 {
		return dst
	}
	gatherAsm(unsafe.Pointer(unsafe.SliceData(dst)), unsafe.Pointer(unsafe.SliceData(parts)), len(parts))
	return dst
}

func gatherPartsForcedGo(dst []byte, parts []string, total int) []byte {
	if cap(dst) < total {
		dst = make([]byte, total)
	} else {
		dst = dst[:total]
	}
	if total == 0 {
		return dst
	}
	gatherGo(unsafe.Pointer(unsafe.SliceData(dst)), unsafe.Pointer(unsafe.SliceData(parts)), len(parts))
	return dst
}

var hybridLiteralSizes = []int{64, 128, 256, 384, 512, 1024, 2048, 4096, 8192}

func hybridFixture(b *testing.B, literalLen int) (*Template, []string, int, []byte) {
	b.Helper()
	e := newTestEngine(b)
	tpl := mustCompile(b, e, buildSource(16, literalLen, "k"))
	pairs := buildPairs(16, "k", 24)
	resolved := make([]string, len(tpl.keys))
	found := make([]bool, len(tpl.keys))
	tpl.resolvePairs(pairs, resolved, found)
	parts, total := tpl.emitParts(make([]string, 0, 2*len(tpl.slots)+1), resolved, found)
	return tpl, parts, total, make([]byte, total)
}

func BenchmarkHybrid_ForcedAsm(b *testing.B) {
	for _, ll := range hybridLiteralSizes {
		b.Run(strconv.Itoa(ll), func(b *testing.B) {
			_, parts, total, dst := hybridFixture(b, ll)
			b.ReportAllocs()
			b.SetBytes(int64(total))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				dst = gatherPartsForcedAsm(dst, parts, total)
			}
			sinkBytes = dst
		})
	}
}

func BenchmarkHybrid_ForcedGo(b *testing.B) {
	for _, ll := range hybridLiteralSizes {
		b.Run(strconv.Itoa(ll), func(b *testing.B) {
			_, parts, total, dst := hybridFixture(b, ll)
			b.ReportAllocs()
			b.SetBytes(int64(total))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				dst = gatherPartsForcedGo(dst, parts, total)
			}
			sinkBytes = dst
		})
	}
}

func BenchmarkHybrid_Dispatch(b *testing.B) {
	for _, ll := range hybridLiteralSizes {
		b.Run(strconv.Itoa(ll), func(b *testing.B) {
			_, parts, total, dst := hybridFixture(b, ll)
			b.ReportAllocs()
			b.SetBytes(int64(total))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				dst = gatherParts(dst, parts, total)
			}
			sinkBytes = dst
		})
	}
}

func BenchmarkHybrid_ReplaceEndToEnd(b *testing.B) {
	for _, ll := range hybridLiteralSizes {
		b.Run(strconv.Itoa(ll), func(b *testing.B) {
			e := newTestEngine(b)
			tpl := mustCompile(b, e, buildSource(16, ll, "k"))
			pairs := buildPairs(16, "k", 24)
			dst := Replace(tpl, nil, pairs)
			b.ReportAllocs()
			b.SetBytes(int64(len(dst)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				dst = Replace(tpl, dst, pairs)
			}
			sinkBytes = dst
		})
	}
}

func TestHybridGatherMatchesBoth(t *testing.T) {
	e := newTestEngine(t)
	for _, ll := range hybridLiteralSizes {
		for _, slots := range []int{2, 8, 16, 64} {
			tpl := mustCompile(t, e, buildSource(slots, ll, "k"))
			pairs := buildPairs(slots, "k", 24)
			resolved := make([]string, len(tpl.keys))
			found := make([]bool, len(tpl.keys))
			tpl.resolvePairs(pairs, resolved, found)
			parts, total := tpl.emitParts(nil, resolved, found)

			viaHybrid := gatherParts(nil, parts, total)
			viaAsm := gatherPartsForcedAsm(nil, parts, total)
			viaGo := gatherPartsForcedGo(nil, parts, total)

			if string(viaHybrid) != string(viaAsm) || string(viaHybrid) != string(viaGo) {
				t.Fatalf("literalLen=%d slots=%d: gather paths disagree", ll, slots)
			}
		}
	}
}
