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

type linearKernel struct {
	name   string
	render func(tpl *Template, base unsafe.Pointer, pos uintptr, end uintptr, values unsafe.Pointer, stride uintptr) uintptr
}

var linearKernels = []linearKernel{
	{"platform", (*Template).renderLinear},
	{"portable", (*Template).writeLinear},
}

type kernelCase struct {
	tpl    *Template
	values []string
	pairs  []string
	want   string
}

func buildKernelCase(t *testing.T, e *Engine, literalLen int, valueLen int, slots int) kernelCase {
	t.Helper()
	var source, want strings.Builder
	c := kernelCase{}
	for i := 0; i <= slots; i++ {
		literal := patternString(literalLen, i)
		source.WriteString(literal)
		want.WriteString(literal)
		if i < slots {
			key := "k" + strconv.Itoa(i)
			value := patternString(valueLen, 100+i)
			source.WriteString("{{" + key + "}}")
			want.WriteString(value)
			c.values = append(c.values, value)
			c.pairs = append(c.pairs, key, value)
		}
	}
	c.tpl = mustCompile(t, e, source.String())
	c.want = want.String()
	return c
}

func TestLinearKernelCopiesEveryLengthClassExactly(t *testing.T) {
	const (
		slots  = 3
		prefix = 5
		spare  = 48
	)
	forEachGatherMode(t, func(t *testing.T) {
		e := newTestEngine(t)
		for _, literalLen := range copyClassLengths {
			for _, valueLen := range copyClassLengths {
				c := buildKernelCase(t, e, literalLen, valueLen, slots)
				total := len(c.want)
				streams := []struct {
					name   string
					start  unsafe.Pointer
					stride uintptr
				}{
					{"values", unsafe.Pointer(unsafe.SliceData(c.values)), stringHeaderSize},
					{"pairs", unsafe.Pointer(&c.pairs[1]), 2 * stringHeaderSize},
				}
				for _, kernel := range linearKernels {
					for _, stream := range streams {
						whole, body := guardedBuffer(prefix + total + spare)
						base := unsafe.Pointer(unsafe.SliceData(body))
						end := kernel.render(c.tpl, base, prefix, uintptr(prefix+total), stream.start, stream.stride)
						label := kernel.name + "/" + stream.name
						if end != uintptr(prefix+total) {
							t.Fatalf("literal %d value %d %s: end %d want %d", literalLen, valueLen, label, end, prefix+total)
						}
						if string(body[prefix:prefix+total]) != c.want {
							t.Fatalf("literal %d value %d %s: output differs", literalLen, valueLen, label)
						}
						for i, b := range body[:prefix] {
							if b != canaryByte {
								t.Fatalf("literal %d value %d %s: byte %d before the output was overwritten", literalLen, valueLen, label, i)
							}
						}
						for i, b := range body[prefix+total:] {
							if b != canaryByte {
								t.Fatalf("literal %d value %d %s: byte %d after the output was overwritten", literalLen, valueLen, label, i)
							}
						}
						assertCanariesIntact(t, whole, prefix+total+spare)
					}
				}
				runtime.KeepAlive(c)
			}
		}
	})
}

func TestLinearKernelStopsWhenAValueOutgrowsTheReservedSpace(t *testing.T) {
	const slots = 3
	forEachGatherMode(t, func(t *testing.T) {
		e := newTestEngine(t)
		for _, literalLen := range copyClassLengths {
			for _, valueLen := range copyClassLengths {
				if valueLen == 0 {
					continue
				}
				c := buildKernelCase(t, e, literalLen, valueLen, slots)
				total := len(c.want)
				for _, kernel := range linearKernels {
					for _, shortfall := range []int{1, valueLen, total} {
						reserved := total - shortfall
						whole, body := guardedBuffer(reserved)
						base := unsafe.Pointer(unsafe.SliceData(whole[canaryLen:]))
						end := kernel.render(c.tpl, base, 0, uintptr(reserved), unsafe.Pointer(unsafe.SliceData(c.values)), stringHeaderSize)
						if end != renderIncomplete {
							t.Fatalf("literal %d value %d %s short by %d: end %d, want an incomplete render", literalLen, valueLen, kernel.name, shortfall, end)
						}
						assertCanariesIntact(t, whole, reserved)
						runtime.KeepAlive(body)
					}
				}
				runtime.KeepAlive(c)
			}
		}
	})
}

func TestLinearKernelReportsAnIncompleteRenderWhenValuesShrinkAfterSizing(t *testing.T) {
	const slots = 3
	forEachGatherMode(t, func(t *testing.T) {
		e := newTestEngine(t)
		for _, literalLen := range copyClassLengths {
			for _, valueLen := range copyClassLengths {
				c := buildKernelCase(t, e, literalLen, valueLen, slots)
				total := len(c.want)
				const extra = 40
				for _, kernel := range linearKernels {
					whole, body := guardedBuffer(total + extra)
					base := unsafe.Pointer(unsafe.SliceData(whole[canaryLen:]))
					end := kernel.render(c.tpl, base, 0, uintptr(total+extra), unsafe.Pointer(unsafe.SliceData(c.values)), stringHeaderSize)
					if end != renderIncomplete || string(body[:total]) != c.want {
						t.Fatalf("literal %d value %d %s: end %d, want an incomplete render of the full text", literalLen, valueLen, kernel.name, end)
					}
					assertCanariesIntact(t, whole, total+extra)
				}
				runtime.KeepAlive(c)
			}
		}
	})
}

type fittingKernel struct {
	name   string
	render func(tpl *Template, dst []byte, values unsafe.Pointer, stride uintptr) uintptr
}

var fittingKernels = []fittingKernel{
	{"platform", (*Template).renderFitting},
	{"portable", (*Template).writeFitting},
}

func TestFittingKernelRendersWheneverTheOutputFits(t *testing.T) {
	const (
		slots  = 3
		prefix = 5
	)
	forEachGatherMode(t, func(t *testing.T) {
		e := newTestEngine(t)
		for _, literalLen := range copyClassLengths {
			for _, valueLen := range copyClassLengths {
				c := buildKernelCase(t, e, literalLen, valueLen, slots)
				total := len(c.want)
				streams := []struct {
					name   string
					start  unsafe.Pointer
					stride uintptr
				}{
					{"values", unsafe.Pointer(unsafe.SliceData(c.values)), stringHeaderSize},
					{"pairs", unsafe.Pointer(&c.pairs[1]), 2 * stringHeaderSize},
				}
				for _, kernel := range fittingKernels {
					for _, stream := range streams {
						for _, spare := range []int{0, 1, 16, 48} {
							whole, body := guardedBuffer(prefix + total + spare)
							end := kernel.render(c.tpl, body[:prefix:len(body)], stream.start, stream.stride)
							label := kernel.name + "/" + stream.name
							if end != uintptr(prefix+total) {
								t.Fatalf("literal %d value %d spare %d %s: end %d want %d", literalLen, valueLen, spare, label, end, prefix+total)
							}
							if string(body[prefix:prefix+total]) != c.want {
								t.Fatalf("literal %d value %d spare %d %s: output differs", literalLen, valueLen, spare, label)
							}
							if bytes.Count(body[:prefix], []byte{canaryByte}) != prefix || bytes.Count(body[prefix+total:], []byte{canaryByte}) != spare {
								t.Fatalf("literal %d value %d spare %d %s: bytes outside the output were overwritten", literalLen, valueLen, spare, label)
							}
							assertCanariesIntact(t, whole, len(body))
						}
					}
				}
				runtime.KeepAlive(c)
			}
		}
	})
}

func TestFittingKernelWritesNothingWhenTheOutputDoesNotFit(t *testing.T) {
	const (
		slots  = 3
		prefix = 5
	)
	forEachGatherMode(t, func(t *testing.T) {
		e := newTestEngine(t)
		for _, literalLen := range copyClassLengths {
			for _, valueLen := range copyClassLengths {
				c := buildKernelCase(t, e, literalLen, valueLen, slots)
				total := len(c.want)
				if total == 0 {
					continue
				}
				for _, kernel := range fittingKernels {
					for _, shortfall := range []int{1, total/2 + 1, total} {
						whole, body := guardedBuffer(prefix + total - shortfall)
						end := kernel.render(c.tpl, body[:prefix:len(body)], unsafe.Pointer(unsafe.SliceData(c.values)), stringHeaderSize)
						if end != renderIncomplete {
							t.Fatalf("literal %d value %d %s short by %d: end %d, want an incomplete render", literalLen, valueLen, kernel.name, shortfall, end)
						}
						if bytes.Count(whole, []byte{canaryByte}) != len(whole) {
							t.Fatalf("literal %d value %d %s short by %d: the buffer was written to", literalLen, valueLen, kernel.name, shortfall)
						}
					}
				}
				runtime.KeepAlive(c)
			}
		}
	})
}

func TestKernelsWriteNothingForATemplateWithoutABody(t *testing.T) {
	const position = 7
	empty := &Template{}
	whole, body := guardedBuffer(64)
	base := unsafe.Pointer(unsafe.SliceData(body))
	for _, kernel := range linearKernels {
		if end := kernel.render(empty, base, position, uintptr(len(body)), nil, stringHeaderSize); end != position {
			t.Fatalf("%s: end %d want %d", kernel.name, end, position)
		}
	}
	for _, kernel := range fittingKernels {
		if end := kernel.render(empty, body[:position:len(body)], nil, stringHeaderSize); end != position {
			t.Fatalf("fitting %s: end %d want %d", kernel.name, end, position)
		}
	}
	if bytes.Count(whole, []byte{canaryByte}) != len(whole) {
		t.Fatal("a template without a body wrote to the buffer")
	}
	if got := AppendValues([]byte("keep"), empty, []string{"x"}); string(got) != "keep" {
		t.Fatalf("AppendValues on a template without a body = %q", got)
	}
}

func TestEmptyOutputIntoANilBufferWritesNothing(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "{{a}}{{b}}")
	values := []string{"", ""}
	stream := unsafe.Pointer(unsafe.SliceData(values))
	for _, kernel := range linearKernels {
		if end := kernel.render(tpl, nil, 0, 0, stream, stringHeaderSize); end != 0 {
			t.Fatalf("%s: end %d want 0", kernel.name, end)
		}
	}
	for _, kernel := range fittingKernels {
		if end := kernel.render(tpl, nil, stream, stringHeaderSize); end != 0 {
			t.Fatalf("fitting %s: end %d want 0", kernel.name, end)
		}
	}
	if got := AppendValues(nil, tpl, values); len(got) != 0 {
		t.Fatalf("AppendValues = %q", got)
	}
	if got := AppendPairs(nil, tpl, []string{"a", "", "b", ""}); len(got) != 0 {
		t.Fatalf("AppendPairs = %q", got)
	}
	if got := ReplaceValues(tpl, values); got != "" {
		t.Fatalf("ReplaceValues = %q", got)
	}
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
			ordered := make([]string, len(c.keys))
			for i, key := range c.keys {
				ordered[i] = c.values[key]
			}
			inputs := []struct {
				name    string
				pairs   []string
				ordered []string
				values  map[string]string
			}{
				{"in-order", c.pairs(inOrder), ordered, c.values},
				{"reversed", c.pairs(reversed), ordered, c.values},
				{"partial", c.pairs(partial), ordered[:len(partial)], partialValues},
				{"extra", withExtra, append(ordered[:len(ordered):len(ordered)], "unused-value"), c.values},
				{"none", nil, nil, nil},
			}
			for _, engine := range []*Engine{cached, uncached} {
				tpl := mustCompile(t, engine, c.source)
				if got := tpl.Keys(); strings.Join(got, itemSeparator) != strings.Join(c.keys, itemSeparator) {
					t.Fatalf("round %d: Keys() = %q want %q", round, got, c.keys)
				}
				for _, input := range inputs {
					want := expectedRender(c.source, input.values)
					for call := 0; call < 2; call++ {
						if got := Replace(tpl, input.pairs); got != want {
							t.Fatalf("round %d %s call %d: Replace differs (len %d want %d)", round, input.name, call, len(got), len(want))
						}
						if got := ReplaceMap(tpl, input.values); got != want {
							t.Fatalf("round %d %s call %d: ReplaceMap differs (len %d want %d)", round, input.name, call, len(got), len(want))
						}
						if got := ReplaceValues(tpl, input.ordered); got != want {
							t.Fatalf("round %d %s call %d: ReplaceValues differs (len %d want %d)", round, input.name, call, len(got), len(want))
						}
					}
					if got := AppendValues([]byte("values|"), tpl, input.ordered); string(got) != "values|"+want {
						t.Fatalf("round %d %s: AppendValues differs", round, input.name)
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

func TestPagesRenderedOnceAreNotKeptInTheCache(t *testing.T) {
	const renders = 30000
	e := newTestEngine(t)
	tpl := mustCompile(t, e, buildSource(1, 2048, "k"))
	for i := 0; i < renders; i++ {
		value := strconv.Itoa(i)
		if got := Replace(tpl, []string{"k0", value}); !strings.Contains(got, value) {
			t.Fatalf("render %d lost its value", i)
		}
	}
	if occupied := occupiedCacheSlots(tpl); occupied != 0 {
		t.Fatalf("%d pages that were only rendered once are held in the cache", occupied)
	}
}

func TestRenderCacheMemoryIsBounded(t *testing.T) {
	const (
		valueSets  = 8 * renderCacheSlots
		literalLen = 2048
		heapSlack  = 64 << 20
	)
	e := newTestEngine(t)
	tpl := mustCompile(t, e, buildSource(1, literalLen, "k"))
	for i := 0; i < valueSets; i++ {
		pairs := []string{"k0", strconv.Itoa(i)}
		for repeat := 0; repeat < 3; repeat++ {
			if got := Replace(tpl, pairs); !strings.Contains(got, pairs[1]) {
				t.Fatalf("render %d lost its value", i)
			}
		}
	}
	if occupied := occupiedCacheSlots(tpl); occupied == 0 || occupied > renderCacheSlots {
		t.Fatalf("cache holds %d entries, limit is %d", occupied, renderCacheSlots)
	}
	var stats runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&stats)
	if limit := uint64(renderCacheSlots*4*literalLen + heapSlack); stats.HeapAlloc > limit {
		t.Fatalf("heap holds %d MB after %d repeated value sets", stats.HeapAlloc>>20, valueSets)
	}
	runtime.KeepAlive(tpl)
}

func TestRenderCacheStoresOnTheSecondRenderAndServesHitsUntilTheTTL(t *testing.T) {
	const ttl = 30 * time.Millisecond
	e := newTestEngine(t, WithRenderCache(ttl))
	tpl := mustCompile(t, e, "<b>{{name}}</b>")
	pairs := []string{"name", "alos"}
	first := Replace(tpl, pairs)
	stored := Replace(tpl, pairs)
	if unsafe.StringData(first) == unsafe.StringData(stored) {
		t.Fatal("a page was cached on its first render")
	}
	if hit := Replace(tpl, pairs); unsafe.StringData(hit) != unsafe.StringData(stored) {
		t.Fatal("third render inside the TTL was not a cache hit")
	}
	deadline := turbo.Now() + int64(2*time.Second)
	for turbo.Now() < deadline {
		if later := Replace(tpl, pairs); unsafe.StringData(later) != unsafe.StringData(stored) {
			if later != first {
				t.Fatalf("re-render after expiry = %q", later)
			}
			if again := Replace(tpl, pairs); unsafe.StringData(again) != unsafe.StringData(later) {
				t.Fatal("the render after expiry was not cached straight away")
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
	pairs := []string{"name", "alos"}
	Replace(tpl, pairs)
	stored := Replace(tpl, pairs)
	if occupied := occupiedCacheSlots(tpl); occupied != 1 {
		t.Fatalf("expected one cached page before clearing, found %d", occupied)
	}
	tpl.ClearRenderCache()
	if occupied := occupiedCacheSlots(tpl); occupied != 0 {
		t.Fatalf("%d entries survived ClearRenderCache", occupied)
	}
	if again := Replace(tpl, pairs); again != stored || unsafe.StringData(again) == unsafe.StringData(stored) {
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
	ordered := make([]string, 0, len(pairs)/2)
	for i := 1; i < len(pairs); i += 2 {
		ordered = append(ordered, pairs[i])
	}
	ReplaceValues(tpl, ordered)
	checks := map[string]func(){
		"cached Replace":       func() { sinkString = Replace(tpl, pairs) },
		"cached ReplaceMap":    func() { sinkString = ReplaceMap(tpl, values) },
		"cached ReplaceValues": func() { sinkString = ReplaceValues(tpl, ordered) },
		"AppendPairs":          func() { buf = AppendPairs(buf[:0], tpl, pairs) },
		"AppendMap":            func() { buf = AppendMap(buf[:0], tpl, values) },
		"AppendValues":         func() { buf = AppendValues(buf[:0], tpl, ordered) },
	}
	for name, run := range checks {
		if allocs := testing.AllocsPerRun(200, run); allocs != 0 {
			t.Fatalf("%s allocates %.1f times per call", name, allocs)
		}
	}
}

type appendForm struct {
	name   string
	render func(dst []byte, tpl *Template, c renderCase, ordered []string) []byte
}

func keyOrder(c renderCase, reversed bool) []int {
	order := make([]int, len(c.keys))
	for i := range order {
		order[i] = i
		if reversed {
			order[i] = len(order) - 1 - i
		}
	}
	return order
}

var appendForms = []appendForm{
	{"values", func(dst []byte, tpl *Template, _ renderCase, ordered []string) []byte {
		return AppendValues(dst, tpl, ordered)
	}},
	{"pairs in key order", func(dst []byte, tpl *Template, c renderCase, _ []string) []byte {
		return AppendPairs(dst, tpl, c.pairs(keyOrder(c, false)))
	}},
	{"pairs reversed", func(dst []byte, tpl *Template, c renderCase, _ []string) []byte {
		return AppendPairs(dst, tpl, c.pairs(keyOrder(c, true)))
	}},
	{"map", func(dst []byte, tpl *Template, c renderCase, _ []string) []byte {
		return AppendMap(dst, tpl, c.values)
	}},
}

func TestAppendNeverTouchesBytesBeyondItsResult(t *testing.T) {
	forEachGatherMode(t, func(t *testing.T) {
		e := newTestEngine(t)
		for round := 0; round < 200; round++ {
			c := buildRenderCase(round)
			tpl := mustCompile(t, e, c.source)
			want := expectedRender(c.source, c.values)
			ordered := make([]string, len(c.keys))
			for i, key := range c.keys {
				ordered[i] = c.values[key]
			}
			for _, spare := range []int{0, 1, 15, 16, 31, 32, 100} {
				for _, shortBy := range []int{0, 1} {
					room := len(want) + spare - shortBy
					if room < 0 {
						continue
					}
					for _, form := range appendForms {
						whole, body := guardedBuffer(3 + room)
						dst := body[:3:len(body)]
						copy(dst, "abc")
						got := form.render(dst, tpl, c, ordered)
						if string(got) != "abc"+want {
							t.Fatalf("round %d spare %d short %d %s: output differs", round, spare, shortBy, form.name)
						}
						fits := len(want) <= room
						if fits != (unsafe.SliceData(got) == unsafe.SliceData(dst)) {
							t.Fatalf("round %d spare %d short %d %s: fits=%v but buffer reuse disagrees", round, spare, shortBy, form.name, fits)
						}
						untouchedFrom := 3
						if fits {
							untouchedFrom = 3 + len(want)
						}
						for i, b := range body[untouchedFrom:] {
							if b != canaryByte {
								t.Fatalf("round %d spare %d short %d %s: byte %d past the result was overwritten", round, spare, shortBy, form.name, untouchedFrom+i)
							}
						}
						assertCanariesIntact(t, whole, 3+room)
						if !fits {
							for i, b := range got[len(got):cap(got)] {
								if b != 0 {
									t.Fatalf("round %d spare %d %s: spare byte %d of a grown buffer is not zero", round, spare, form.name, i)
								}
							}
						}
					}
				}
			}
		}
	})
}

func TestRenderRecoversWhenValuesChangeAfterSizing(t *testing.T) {
	const source = "<a>{{x}}</a><b>{{y}}</b>"
	e := newTestEngine(t)
	tpl := mustCompile(t, e, source)
	values := []string{"first", strings.Repeat("y", 300)}
	want := expectedRender(source, map[string]string{"x": values[0], "y": values[1]})
	dst := make([]byte, 4, 1024)
	copy(dst, "head")
	got := tpl.appendSnapshot(dst, unsafe.Pointer(unsafe.SliceData(values)), stringHeaderSize)
	if string(got) != "head"+want {
		t.Fatalf("snapshot render = %q", got)
	}
}
