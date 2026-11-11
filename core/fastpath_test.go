package core

import (
	"bytes"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/guno1928/turbo"
)

const (
	canaryByte    = 0xA5
	canaryLen     = 256
	itemSeparator = "|next|"
)

var copyClassLengths = []int{0, 1, 2, 3, 4, 5, 7, 8, 9, 15, 16, 17, 31, 32, 33, 63, 64, 65, 127, 128, 129, 191, 255, 256, 257, 511, 1000, 4095, 4096, 4097, 9000}

func patternString(length int, salt int) string {
	raw := make([]byte, length)
	for i := range raw {
		raw[i] = byte('a' + (i*7+salt*13)%26)
	}
	return string(raw)
}

func guardedBuffer(size int) (whole []byte, body []byte) {
	whole = bytes.Repeat([]byte{canaryByte}, size+2*canaryLen)
	return whole, whole[canaryLen : canaryLen+size]
}

func assertCanariesIntact(t *testing.T, whole []byte, size int) {
	t.Helper()
	for i := 0; i < canaryLen; i++ {
		if whole[i] != canaryByte || whole[canaryLen+size+i] != canaryByte {
			t.Fatalf("write outside the output buffer near offset %d (size %d)", i, size)
		}
	}
}

func TestGatherStreamsCopiesEveryLengthClassExactly(t *testing.T) {
	forEachGatherMode(t, func(t *testing.T) {
		for _, literalLen := range copyClassLengths {
			for _, valueLen := range copyClassLengths {
				const slots = 3
				literals := make([]string, slots+1)
				values := make([]string, slots)
				pairs := make([]string, 0, slots*2)
				var want strings.Builder
				for i := 0; i <= slots; i++ {
					literals[i] = patternString(literalLen, i)
					want.WriteString(literals[i])
					if i < slots {
						values[i] = patternString(valueLen, 100+i)
						pairs = append(pairs, "key", values[i])
						want.WriteString(values[i])
					}
				}
				total := want.Len()
				for _, layout := range []struct {
					name   string
					start  unsafe.Pointer
					stride uintptr
				}{
					{"contiguous", unsafe.Pointer(unsafe.SliceData(values)), stringHeaderSize},
					{"pairs", unsafe.Pointer(&pairs[1]), 2 * stringHeaderSize},
				} {
					whole, body := guardedBuffer(total)
					base := unsafe.Pointer(unsafe.SliceData(whole[canaryLen:]))
					if written := int(gatherStreamsAsm(base, uintptr(total), unsafe.Pointer(unsafe.SliceData(literals)), stringHeaderSize, layout.start, layout.stride, 2*slots+1)); written != total {
						t.Fatalf("literal %d value %d %s: wrote %d bytes want %d", literalLen, valueLen, layout.name, written, total)
					}
					if string(body) != want.String() {
						t.Fatalf("literal %d value %d %s: output differs", literalLen, valueLen, layout.name)
					}
					assertCanariesIntact(t, whole, total)
				}
				runtime.KeepAlive(pairs)
			}
		}
	})
}

func TestGatherStreamsNeverWritesPastTheLimit(t *testing.T) {
	forEachGatherMode(t, func(t *testing.T) {
		for _, segmentLen := range copyClassLengths {
			if segmentLen == 0 {
				continue
			}
			literals := []string{patternString(segmentLen, 1), patternString(segmentLen, 2)}
			values := []string{patternString(segmentLen, 3)}
			for _, room := range []int{0, 1, segmentLen - 1, segmentLen, 2*segmentLen - 1, 2 * segmentLen, 3*segmentLen - 1} {
				whole, _ := guardedBuffer(room)
				base := unsafe.Pointer(unsafe.SliceData(whole[canaryLen:]))
				written := int(gatherStreamsAsm(base, uintptr(room), unsafe.Pointer(unsafe.SliceData(literals)), stringHeaderSize, unsafe.Pointer(unsafe.SliceData(values)), stringHeaderSize, 3))
				if want := room / segmentLen * segmentLen; written != want {
					t.Fatalf("segment %d room %d: wrote %d bytes want %d", segmentLen, room, written, want)
				}
				assertCanariesIntact(t, whole, room)
			}
		}
	})
}

type renderCase struct {
	source string
	keys   []string
	values map[string]string
}

func buildRenderCase(round int) renderCase {
	next := uint64(round)*0x9E3779B97F4A7C15 + 1
	random := func(limit int) int {
		next ^= next << 13
		next ^= next >> 7
		next ^= next << 17
		return int(next % uint64(limit))
	}
	slots := random(40)
	distinct := slots
	if slots > 1 && random(3) == 0 {
		distinct = 1 + random(slots)
	}
	keys := make([]string, distinct)
	values := make(map[string]string, distinct)
	for i := range keys {
		keys[i] = "k" + strconv.Itoa(i) + strings.Repeat("x", random(11))
		values[keys[i]] = patternString(copyClassLengths[random(len(copyClassLengths))], i)
	}
	var source strings.Builder
	for i := 0; i < slots; i++ {
		source.WriteString(patternString(copyClassLengths[random(len(copyClassLengths)-4)], 50+i))
		source.WriteString("{{" + keys[i%distinct] + "}}")
	}
	source.WriteString(patternString(copyClassLengths[random(len(copyClassLengths)-4)], 99))
	return renderCase{source: source.String(), keys: keys, values: values}
}

func (c renderCase) pairs(order []int) []string {
	pairs := make([]string, 0, len(order)*2)
	for _, index := range order {
		pairs = append(pairs, c.keys[index], c.values[c.keys[index]])
	}
	return pairs
}

func TestEveryRenderPathMatchesTheReferenceRenderer(t *testing.T) {
	forEachGatherMode(t, func(t *testing.T) {
		cached := newTestEngine(t)
		uncached := newTestEngine(t, WithRenderCache(RenderCacheDisabled))
		for round := 0; round < 300; round++ {
			c := buildRenderCase(round)
			inOrder := make([]int, len(c.keys))
			reversed := make([]int, len(c.keys))
			for i := range inOrder {
				inOrder[i] = i
				reversed[i] = len(c.keys) - 1 - i
			}
			partial := inOrder[:len(inOrder)/2]
			partialValues := make(map[string]string, len(partial))
			for _, index := range partial {
				partialValues[c.keys[index]] = c.values[c.keys[index]]
			}
			withExtra := append(c.pairs(inOrder), "unused-key", "unused-value")
			inputs := []struct {
				name   string
				pairs  []string
				values map[string]string
			}{
				{"in-order", c.pairs(inOrder), c.values},
				{"reversed", c.pairs(reversed), c.values},
				{"partial", c.pairs(partial), partialValues},
				{"extra", withExtra, c.values},
				{"none", nil, nil},
			}
			for _, engine := range []*Engine{cached, uncached} {
				tpl := mustCompile(t, engine, c.source)
				for _, input := range inputs {
					want := expectedRender(c.source, input.values)
					for call := 0; call < 2; call++ {
						if got := Replace(tpl, input.pairs); got != want {
							t.Fatalf("round %d %s call %d: Replace differs (len %d want %d)", round, input.name, call, len(got), len(want))
						}
						if got := ReplaceMap(tpl, input.values); got != want {
							t.Fatalf("round %d %s call %d: ReplaceMap differs (len %d want %d)", round, input.name, call, len(got), len(want))
						}
					}
					prefix := []byte("prefix|")
					if got := AppendPairs(prefix, tpl, input.pairs); string(got) != "prefix|"+want {
						t.Fatalf("round %d %s: AppendPairs differs", round, input.name)
					}
					roomy := make([]byte, 3, len(want)+64)
					copy(roomy, "abc")
					got := AppendMap(roomy, tpl, input.values)
					if string(got) != "abc"+want {
						t.Fatalf("round %d %s: AppendMap differs", round, input.name)
					}
					if unsafe.SliceData(got) != unsafe.SliceData(roomy) {
						t.Fatalf("round %d %s: AppendMap reallocated despite spare capacity", round, input.name)
					}
				}
			}
		}
	})
}

func TestAppendKeepsExistingBytesAndNeverWritesPastTheResult(t *testing.T) {
	const source = "<h1>{{title}}</h1><p>{{body}}</p>"
	e := newTestEngine(t)
	tpl := mustCompile(t, e, source)
	body := strings.Repeat("b", 300)
	pairs := []string{"title", "Hello", "body", body}
	want := expectedRender(source, map[string]string{"title": "Hello", "body": body})
	whole, guarded := guardedBuffer(len(want) + 8)
	dst := guarded[:8]
	copy(dst, "12345678")
	out := AppendPairs(dst, tpl, pairs)
	if string(out) != "12345678"+want {
		t.Fatalf("append output = %q", out)
	}
	assertCanariesIntact(t, whole, len(want)+8)
	if got := AppendPairs(nil, nil, pairs); got != nil {
		t.Fatalf("nil template should return dst unchanged, got %q", got)
	}
}

func TestStaticTemplateIsReturnedWithoutCopying(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "no placeholders here")
	first := Replace(tpl, nil)
	second := ReplaceMap(tpl, map[string]string{"x": "y"})
	if first != "no placeholders here" || second != first {
		t.Fatalf("static render = %q / %q", first, second)
	}
	if unsafe.StringData(first) != unsafe.StringData(second) {
		t.Fatal("static template was copied")
	}
}

func TestHashStringsSeparatesDifferentInputs(t *testing.T) {
	seen := make(map[uint64]string, 32768)
	record := func(items ...string) {
		t.Helper()
		identity := strconv.Itoa(len(items)) + itemSeparator + strings.Join(items, itemSeparator)
		key := hashStrings(pairsHashSeed, items)
		if previous, dup := seen[key]; dup && previous != identity {
			t.Fatalf("%q and %q hash to the same key", previous, identity)
		}
		seen[key] = identity
	}
	record()
	record("")
	record("", "")
	record("ab", "c")
	record("a", "bc")
	record("abc")
	record("abc", "")
	record("", "abc")
	for length := 1; length <= 80; length++ {
		base := []byte(patternString(length, length))
		record(string(base))
		record(string(base[:length/2]), string(base[length/2:]))
		for position := 0; position < length; position++ {
			flipped := append([]byte(nil), base...)
			flipped[position] ^= 1
			record(string(flipped))
		}
		record(strings.Repeat("A", length))
	}
	for i := 0; i < 20000; i++ {
		record("user", strconv.Itoa(i), "page", strconv.Itoa(i%7))
	}
}

func TestMapHashDistinguishesMissingFromEmptyAndIgnoresUnusedKeys(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "{{a}}-{{b}}")
	missing := tpl.hashMapValues(map[string]string{"a": "1"})
	empty := tpl.hashMapValues(map[string]string{"a": "1", "b": ""})
	if missing == empty {
		t.Fatal("a missing key and an empty value hash to the same key")
	}
	withUnused := tpl.hashMapValues(map[string]string{"a": "1", "b": "", "zzz": "ignored"})
	if withUnused != empty {
		t.Fatal("a key the template never uses changed the cache key")
	}
	if got := ReplaceMap(tpl, map[string]string{"a": "1"}); got != "1-{{b}}" {
		t.Fatalf("missing render = %q", got)
	}
	if got := ReplaceMap(tpl, map[string]string{"a": "1", "b": ""}); got != "1-" {
		t.Fatalf("empty render = %q", got)
	}
}

func legacyCollidingValue(value string, replacementPrefix string) string {
	const multiplier = 0xff51afd7ed558ccd
	word := func(s string) uint64 {
		return loadLE64(unsafe.Pointer(unsafe.StringData(s)))
	}
	start := uint64(0x9E3779B97F4A7C15) ^ 16
	target := ((start ^ word(value[:8])) * multiplier) ^ word(value[8:])
	forged := make([]byte, 16)
	copy(forged, replacementPrefix)
	tail := ((start ^ word(replacementPrefix)) * multiplier) ^ target
	for i := 0; i < 8; i++ {
		forged[8+i] = byte(tail >> (8 * i))
	}
	return string(forged)
}

func TestValuesCraftedAgainstTheOldHashNoLongerShareACacheEntry(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "<p>{{msg}}</p>")
	victim := "AAAAAAAABBBBBBBB"
	forged := legacyCollidingValue(victim, "evilevil")
	if hashPlaceholderKey(victim) != hashPlaceholderKey(forged) {
		t.Fatal("test setup: forged value does not collide under the old hash")
	}
	first := Replace(tpl, []string{"msg", victim})
	second := Replace(tpl, []string{"msg", forged})
	if first != "<p>"+victim+"</p>" || second != "<p>"+forged+"</p>" {
		t.Fatalf("a crafted value was served another value's page: %q / %q", first, second)
	}
}

func occupiedCacheSlots(tpl *Template) int {
	cache := tpl.renderCache.Load()
	if cache == nil {
		return 0
	}
	occupied := 0
	for i := range cache.slots {
		if cache.slots[i].Load() != nil {
			occupied++
		}
	}
	return occupied
}

func TestRenderCacheMemoryIsBounded(t *testing.T) {
	const (
		renders    = 30000
		literalLen = 2048
		heapSlack  = 64 << 20
	)
	e := newTestEngine(t)
	tpl := mustCompile(t, e, buildSource(1, literalLen, "k"))
	for i := 0; i < renders; i++ {
		value := strconv.Itoa(i)
		if got := Replace(tpl, []string{"k0", value}); !strings.Contains(got, value) {
			t.Fatalf("render %d lost its value", i)
		}
	}
	if occupied := occupiedCacheSlots(tpl); occupied == 0 || occupied > renderCacheSlots {
		t.Fatalf("cache holds %d entries, limit is %d", occupied, renderCacheSlots)
	}
	var stats runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&stats)
	if limit := uint64(renderCacheSlots*4*literalLen + heapSlack); stats.HeapAlloc > limit {
		t.Fatalf("heap holds %d MB after %d distinct renders", stats.HeapAlloc>>20, renders)
	}
	runtime.KeepAlive(tpl)
}

func TestRenderCacheServesHitsUntilTheTTLThenRendersAgain(t *testing.T) {
	const ttl = 30 * time.Millisecond
	e := newTestEngine(t, WithRenderCache(ttl))
	tpl := mustCompile(t, e, "<b>{{name}}</b>")
	pairs := []string{"name", "alos"}
	first := Replace(tpl, pairs)
	if second := Replace(tpl, pairs); unsafe.StringData(first) != unsafe.StringData(second) {
		t.Fatal("second render inside the TTL was not a cache hit")
	}
	deadline := turbo.Now() + int64(2*time.Second)
	for turbo.Now() < deadline {
		if later := Replace(tpl, pairs); unsafe.StringData(later) != unsafe.StringData(first) {
			if later != first {
				t.Fatalf("re-render after expiry = %q", later)
			}
			return
		}
		turbo.Sleep(5 * time.Millisecond)
	}
	t.Fatal("cached render never expired")
}

func TestClearRenderCacheDropsEveryEntry(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "<b>{{name}}</b>")
	first := Replace(tpl, []string{"name", "alos"})
	tpl.ClearRenderCache()
	if occupied := occupiedCacheSlots(tpl); occupied != 0 {
		t.Fatalf("%d entries survived ClearRenderCache", occupied)
	}
	if again := Replace(tpl, []string{"name", "alos"}); again != first || unsafe.StringData(again) == unsafe.StringData(first) {
		t.Fatal("render after ClearRenderCache was served from the cache")
	}
}

func TestRenderCacheStaysCorrectWhenManyValueSetsEvictEachOther(t *testing.T) {
	const (
		valueSets = 4 * renderCacheSlots
		workers   = 8
		rounds    = 3
	)
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "[{{a}}|{{b}}]")
	var wg sync.WaitGroup
	failures := make(chan string, workers)
	for worker := 0; worker < workers; worker++ {
		wg.Go(func() {
			for round := 0; round < rounds; round++ {
				for i := worker; i < valueSets; i += 3 {
					a, b := strconv.Itoa(i), strconv.Itoa(i*31)
					want := "[" + a + "|" + b + "]"
					got := Replace(tpl, []string{"a", a, "b", b})
					if got == want {
						got = ReplaceMap(tpl, map[string]string{"a": a, "b": b})
					}
					if got != want {
						failures <- got + " want " + want
						return
					}
				}
			}
		})
	}
	wg.Wait()
	close(failures)
	for failure := range failures {
		t.Fatalf("wrong page served: %s", failure)
	}
}

func TestHotRenderPathsDoNotAllocate(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are not meaningful under the race detector")
	}
	e := newTestEngine(t)
	tpl := mustCompile(t, e, buildSource(8, 64, "k"))
	pairs := buildPairs(8, "k", 12)
	values := buildValues(8, "k", 12)
	Replace(tpl, pairs)
	ReplaceMap(tpl, values)
	buf := AppendPairs(nil, tpl, pairs)
	checks := map[string]func(){
		"cached Replace":    func() { sinkString = Replace(tpl, pairs) },
		"cached ReplaceMap": func() { sinkString = ReplaceMap(tpl, values) },
		"AppendPairs":       func() { buf = AppendPairs(buf[:0], tpl, pairs) },
		"AppendMap":         func() { buf = AppendMap(buf[:0], tpl, values) },
	}
	for name, run := range checks {
		if allocs := testing.AllocsPerRun(200, run); allocs != 0 {
			t.Fatalf("%s allocates %.1f times per call", name, allocs)
		}
	}
}
