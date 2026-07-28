package core

import (
	"strings"
	"testing"
	"unsafe"
)

// Alternatives for the output-writing step of Replace, which interleaves
// literals and resolved values into dst. runtime.memmove was 17.7% of
// BenchmarkReplaceRealisticHTMLPairs, and the surrounding Go loop pays a bounds
// check and slice-header rebuild per segment.

// Alternative A: the current production loop using the builtin copy.
func altCopyBuiltin(dst []byte, literals []string, values []string) []byte {
	pos := 0
	for i := range values {
		pos += copy(dst[pos:], literals[i])
		pos += copy(dst[pos:], values[i])
	}
	copy(dst[pos:], literals[len(literals)-1])
	return dst
}

// Alternative B: unsafe pointer walk calling runtime.memmove directly, which
// removes the per-segment bounds check and slice-header construction.
func altCopyMemmove(dst []byte, literals []string, values []string) []byte {
	base := unsafe.Pointer(unsafe.SliceData(dst))
	pos := uintptr(0)
	for i := range values {
		if n := uintptr(len(literals[i])); n != 0 {
			runtimeMemmove(unsafe.Add(base, pos), unsafe.Pointer(unsafe.StringData(literals[i])), n)
			pos += n
		}
		if n := uintptr(len(values[i])); n != 0 {
			runtimeMemmove(unsafe.Add(base, pos), unsafe.Pointer(unsafe.StringData(values[i])), n)
			pos += n
		}
	}
	last := literals[len(literals)-1]
	if n := uintptr(len(last)); n != 0 {
		runtimeMemmove(unsafe.Add(base, pos), unsafe.Pointer(unsafe.StringData(last)), n)
	}
	return dst
}

// Alternative C: build a segment table then concatenate it with the Go gather.
func altCopySegsGo(dst []byte, segs []gatherSeg, literals []string, values []string) []byte {
	segs = segs[:0]
	for i := range values {
		segs = append(segs,
			gatherSeg{ptr: unsafe.Pointer(unsafe.StringData(literals[i])), n: uintptr(len(literals[i]))},
			gatherSeg{ptr: unsafe.Pointer(unsafe.StringData(values[i])), n: uintptr(len(values[i]))})
	}
	last := literals[len(literals)-1]
	segs = append(segs, gatherSeg{ptr: unsafe.Pointer(unsafe.StringData(last)), n: uintptr(len(last))})
	gatherGo(unsafe.Pointer(unsafe.SliceData(dst)), unsafe.Pointer(unsafe.SliceData(segs)), len(segs))
	return dst
}

// Alternative D: build a segment table then concatenate it with the hand-written
// Plan 9 assembly gather.
func altCopySegsAsm(dst []byte, segs []gatherSeg, literals []string, values []string) []byte {
	segs = segs[:0]
	for i := range values {
		segs = append(segs,
			gatherSeg{ptr: unsafe.Pointer(unsafe.StringData(literals[i])), n: uintptr(len(literals[i]))},
			gatherSeg{ptr: unsafe.Pointer(unsafe.StringData(values[i])), n: uintptr(len(values[i]))})
	}
	last := literals[len(literals)-1]
	segs = append(segs, gatherSeg{ptr: unsafe.Pointer(unsafe.StringData(last)), n: uintptr(len(last))})
	gatherAsm(unsafe.Pointer(unsafe.SliceData(dst)), unsafe.Pointer(unsafe.SliceData(segs)), len(segs))
	return dst
}

// Alternative E: inline length-classed copy in pure Go, avoiding a memmove call
// for the short literals that dominate real templates.
func altCopyInlineClassed(dst []byte, literals []string, values []string) []byte {
	pos := 0
	emit := func(s string) {
		n := len(s)
		if n == 0 {
			return
		}
		if n <= 16 {
			copy(dst[pos:pos+n], s)
		} else {
			runtimeMemmove(unsafe.Add(unsafe.Pointer(unsafe.SliceData(dst)), uintptr(pos)),
				unsafe.Pointer(unsafe.StringData(s)), uintptr(n))
		}
		pos += n
	}
	for i := range values {
		emit(literals[i])
		emit(values[i])
	}
	emit(literals[len(literals)-1])
	return dst
}

func altCopyFixture(slots int, literalLen int, valueLen int) (literals []string, values []string, total int) {
	literal := strings.Repeat("L", literalLen)
	value := strings.Repeat("V", valueLen)
	literals = make([]string, slots+1)
	values = make([]string, slots)
	for i := range literals {
		literals[i] = literal
	}
	for i := range values {
		values[i] = value
	}
	total = literalLen*(slots+1) + valueLen*slots
	return
}

func altCopyExpected(literals []string, values []string) string {
	var b strings.Builder
	for i := range values {
		b.WriteString(literals[i])
		b.WriteString(values[i])
	}
	b.WriteString(literals[len(literals)-1])
	return b.String()
}

func TestAltCopyEquivalence(t *testing.T) {
	cases := []struct{ slots, litLen, valLen int }{
		{1, 0, 0}, {1, 1, 1}, {1, 3, 5}, {1, 7, 8}, {1, 8, 9},
		{2, 15, 16}, {2, 16, 17}, {3, 31, 32}, {3, 32, 33},
		{4, 63, 64}, {4, 64, 65}, {8, 100, 200}, {16, 24, 12},
		{32, 5, 3}, {64, 40, 40}, {2, 0, 10}, {2, 10, 0},
	}
	for _, c := range cases {
		literals, values, total := altCopyFixture(c.slots, c.litLen, c.valLen)
		want := altCopyExpected(literals, values)
		if len(want) != total {
			t.Fatalf("fixture length mismatch: %d vs %d", len(want), total)
		}
		segs := make([]gatherSeg, 0, len(literals)+len(values))

		check := func(label string, got []byte) {
			if string(got) != want {
				t.Fatalf("slots=%d lit=%d val=%d alt=%s mismatch\n got %q\nwant %q",
					c.slots, c.litLen, c.valLen, label, got, want)
			}
		}
		check("builtin", altCopyBuiltin(make([]byte, total), literals, values))
		check("memmove", altCopyMemmove(make([]byte, total), literals, values))
		check("segsgo", altCopySegsGo(make([]byte, total), segs, literals, values))
		check("segsasm", altCopySegsAsm(make([]byte, total), segs, literals, values))
		check("inlineclassed", altCopyInlineClassed(make([]byte, total), literals, values))
	}
}

func TestGatherAsmMatchesGo(t *testing.T) {
	if !gatherAsmAvailable {
		t.Skip("assembly gather not built on this platform")
	}
	for _, n := range []int{0, 1, 2, 3, 4, 5, 7, 8, 9, 15, 16, 17, 31, 32, 33, 63, 64, 65, 127, 128, 4096} {
		src := make([]byte, n)
		for i := range src {
			src[i] = byte('a' + i%26)
		}
		segs := []gatherSeg{{ptr: unsafe.Pointer(unsafe.SliceData(src)), n: uintptr(n)}}
		if n == 0 {
			segs[0].ptr = unsafe.Pointer(&src)
		}
		goDst := make([]byte, n+8)
		asmDst := make([]byte, n+8)
		gatherGo(unsafe.Pointer(unsafe.SliceData(goDst)), unsafe.Pointer(unsafe.SliceData(segs)), len(segs))
		gatherAsm(unsafe.Pointer(unsafe.SliceData(asmDst)), unsafe.Pointer(unsafe.SliceData(segs)), len(segs))
		if string(goDst) != string(asmDst) {
			t.Fatalf("n=%d asm != go\n asm %q\n  go %q", n, asmDst, goDst)
		}
		for i := n; i < n+8; i++ {
			if asmDst[i] != 0 {
				t.Fatalf("n=%d assembly wrote past the segment end at %d", n, i)
			}
		}
	}
}

func TestGatherAsmMultiSegment(t *testing.T) {
	if !gatherAsmAvailable {
		t.Skip("assembly gather not built on this platform")
	}
	parts := []string{"", "a", "bc", "def", strings.Repeat("x", 40), "", strings.Repeat("y", 200), "z"}
	segs := make([]gatherSeg, 0, len(parts))
	total := 0
	var want strings.Builder
	for _, p := range parts {
		segs = append(segs, gatherSeg{ptr: unsafe.Pointer(unsafe.StringData(p)), n: uintptr(len(p))})
		total += len(p)
		want.WriteString(p)
	}
	dst := make([]byte, total+16)
	gatherAsm(unsafe.Pointer(unsafe.SliceData(dst)), unsafe.Pointer(unsafe.SliceData(segs)), len(segs))
	if string(dst[:total]) != want.String() {
		t.Fatalf("asm multi-segment mismatch\n got %q\nwant %q", dst[:total], want.String())
	}
	for i := total; i < len(dst); i++ {
		if dst[i] != 0 {
			t.Fatalf("assembly overwrote tail at %d", i)
		}
	}
}

func benchCopy(b *testing.B, slots, litLen, valLen int) {
	literals, values, total := altCopyFixture(slots, litLen, valLen)
	dst := make([]byte, total)
	segs := make([]gatherSeg, 0, len(literals)+len(values))

	b.Run("A_builtin", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			benchSink = altCopyBuiltin(dst, literals, values)
		}
	})
	b.Run("B_memmove", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			benchSink = altCopyMemmove(dst, literals, values)
		}
	})
	b.Run("C_segsgo", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			benchSink = altCopySegsGo(dst, segs, literals, values)
		}
	})
	b.Run("D_segsasm", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			benchSink = altCopySegsAsm(dst, segs, literals, values)
		}
	})
	b.Run("E_inlineclassed", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			benchSink = altCopyInlineClassed(dst, literals, values)
		}
	})
}

func BenchmarkAltCopyShortLiterals(b *testing.B)  { benchCopy(b, 14, 20, 12) }
func BenchmarkAltCopyTinyLiterals(b *testing.B)   { benchCopy(b, 16, 6, 4) }
func BenchmarkAltCopyMediumLiterals(b *testing.B) { benchCopy(b, 8, 60, 40) }
func BenchmarkAltCopyLongLiterals(b *testing.B)   { benchCopy(b, 4, 512, 256) }
func BenchmarkAltCopyManySlots(b *testing.B)      { benchCopy(b, 64, 24, 12) }
