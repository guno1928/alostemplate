package core

import (
	"strings"
	"testing"
)

var (
	fuzzCachedEngine   = NewEngine()
	fuzzUncachedEngine = NewEngine(WithRenderCache(RenderCacheDisabled))
)

type fuzzInputs struct {
	lookup  map[string]string
	pairs   []string
	inOrder []string
}

func buildFuzzInputs(keys []string, pool []string, count int) fuzzInputs {
	inputs := fuzzInputs{lookup: make(map[string]string, count)}
	for i, key := range keys[:count] {
		value := pool[i%len(pool)]
		inputs.lookup[key] = value
		inputs.pairs = append(inputs.pairs, key, value)
		inputs.inOrder = append(inputs.inOrder, value)
	}
	return inputs
}

func checkEveryRenderForm(t *testing.T, tpl *Template, inputs fuzzInputs, want string, stage string) {
	t.Helper()
	const prefix = "kept:"
	checks := []struct {
		name string
		got  string
	}{
		{"ReplaceMap", ReplaceMap(tpl, inputs.lookup)},
		{"Replace", Replace(tpl, inputs.pairs)},
		{"ReplaceValues", ReplaceValues(tpl, inputs.inOrder)},
		{"AppendMap", strings.TrimPrefix(string(AppendMap([]byte(prefix), tpl, inputs.lookup)), prefix)},
		{"AppendPairs", strings.TrimPrefix(string(AppendPairs([]byte(prefix), tpl, inputs.pairs)), prefix)},
		{"AppendValues", strings.TrimPrefix(string(AppendValues([]byte(prefix), tpl, inputs.inOrder)), prefix)},
	}
	for _, check := range checks {
		if check.got != want {
			t.Fatalf("%s %s rendered %q want %q", stage, check.name, check.got, want)
		}
	}
}

func FuzzRenderMatchesReference(f *testing.F) {
	f.Add("", "", "", "")
	f.Add("plain text", "a", "b", "c")
	f.Add("{{a}}", "value", "", "x")
	f.Add("<h1>{{user}}</h1><p>{{email}}</p><i>{{token}}</i>", "bob", "bob@example.test", "t")
	f.Add("{{a}}{{b}}{{a}}{{ a }}", "1", "22", "333")
	f.Add("{{a}}|{{b}}|{{c}}|{{d}}|{{e}}|{{f}}|{{g}}|{{h}}|{{i}}|{{j}}|{{k}}|{{l}}|{{m}}|{{n}}|{{o}}|{{p}}|{{q}}|{{r}}", "x", strings.Repeat("y", 40), strings.Repeat("z", 300))
	f.Add(strings.Repeat("L", 70)+"{{k}}"+strings.Repeat("R", 33), strings.Repeat("v", 17), "", "")
	f.Add("{{ {{a}} }}", "{{a}}", "}}", "{{")
	f.Add("{{include \"nav\"}} and {{b}}", "n", "b", "")
	f.Fuzz(func(t *testing.T, source string, first string, second string, third string) {
		pool := []string{first, second, third}
		for _, e := range []*Engine{fuzzCachedEngine, fuzzUncachedEngine} {
			tpl, err := e.compileSource(source)
			if err != nil {
				return
			}
			keys := tpl.Keys()
			if got := ReplaceMap(tpl, nil); got != source {
				t.Fatalf("render with no values = %q want the source %q", got, source)
			}
			for _, count := range []int{len(keys), len(keys) / 2, 0} {
				inputs := buildFuzzInputs(keys, pool, count)
				want := expectedRender(source, inputs.lookup)
				for _, stage := range []string{"first", "second", "third"} {
					checkEveryRenderForm(t, tpl, inputs, want, stage)
				}
			}
		}
	})
}

func FuzzCustomDelimitersRoundTripTheSource(f *testing.F) {
	f.Add("{{", "}}", "a {{b}} c")
	f.Add("<%", "%>", "a <%b%> c {{d}}")
	f.Add("[", "]", "[a][b][a]")
	f.Add("|", "|", "|a||b|")
	f.Add("aa", "a", "aaaaxaaa")
	f.Add("", "", "{{x}}")
	f.Fuzz(func(t *testing.T, left string, right string, source string) {
		e := NewEngine(WithDelimiters(left, right))
		defer e.Stop()
		tpl, err := e.compileSource(source)
		if err != nil {
			return
		}
		if got := ReplaceMap(tpl, nil); got != source {
			t.Fatalf("delimiters %q %q: ReplaceMap with no values = %q want %q", left, right, got, source)
		}
		if got := string(AppendValues(nil, tpl, nil)); got != source {
			t.Fatalf("delimiters %q %q: AppendValues with no values = %q want %q", left, right, got, source)
		}
		if got := Replace(tpl, nil); got != source {
			t.Fatalf("delimiters %q %q: Replace with no pairs = %q want %q", left, right, got, source)
		}
		keys := tpl.Keys()
		echoed := make([]string, 0, len(keys))
		for range keys {
			echoed = append(echoed, "")
		}
		rendered := ReplaceValues(tpl, echoed)
		if len(rendered) > len(source) {
			t.Fatalf("delimiters %q %q: rendering empty values grew the page from %d to %d bytes", left, right, len(source), len(rendered))
		}
	})
}

func FuzzHashStringsSeparatesDifferentInputs(f *testing.F) {
	f.Add("", "", "", "")
	f.Add("a", "b", "ab", "")
	f.Add("ab", "c", "a", "bc")
	f.Add(strings.Repeat("x", 16), "y", strings.Repeat("x", 17), "y")
	f.Add(strings.Repeat("x", 64), "", strings.Repeat("x", 65), "")
	f.Add(strings.Repeat("x", 100), "", strings.Repeat("x", 228), "")
	f.Fuzz(func(t *testing.T, a0 string, a1 string, b0 string, b1 string) {
		left := []string{a0, a1}
		right := []string{b0, b1}
		same := describeItems(left) == describeItems(right)
		for _, seed := range []uint64{pairsHashSeed, valuesHashSeed, mapHashSeed} {
			if (hashStrings(seed, left) == hashStrings(seed, right)) != same {
				t.Fatalf("hash equality %v disagrees with input equality %v for %q and %q", !same, same, left, right)
			}
			if hashStrings(seed, left) == hashStrings(seed, []string{a0 + a1}) {
				t.Fatalf("two items %q hash like their concatenation", left)
			}
			if hashStrings(seed, left) == hashStrings(seed, left[:1]) {
				t.Fatalf("dropping the second item of %q did not change the hash", left)
			}
		}
	})
}
