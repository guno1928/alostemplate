package core

import (
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"
)

const (
	accountPageSource     = "<h1>{{user}}</h1><p>{{email}}</p><i>{{token}}</i>"
	isolationAccounts     = 4000
	rendersPerValueSet    = 3
	revisitStride         = 37
	longestValueLength    = 300
	lengthSecretPeriod    = 128
	valueFlipStride       = 5
	boundaryShiftText     = "abcdefghijklmnopqrstuvwx"
	boundaryShiftSource   = "{{a}}|{{b}}|{{c}}"
	reusedSliceRounds     = 1000
	reusedSliceVariants   = 50
	largestRenderCacheTTL = time.Duration(math.MaxInt64)
)

type accountPage struct {
	values  map[string]string
	pairs   []string
	inOrder []string
	want    string
}

func buildAccountPage(id int) accountPage {
	user := "user-" + strconv.Itoa(id)
	email := user + "@example.test"
	token := strings.Repeat(strconv.Itoa(id%10), 1+id%90)
	values := map[string]string{"user": user, "email": email, "token": token}
	return accountPage{
		values:  values,
		pairs:   []string{"user", user, "email", email, "token", token},
		inOrder: []string{user, email, token},
		want:    expectedRender(accountPageSource, values),
	}
}

func sharesBacking(a string, b string) bool {
	return len(a) == len(b) && unsafe.StringData(a) == unsafe.StringData(b)
}

func checkAccountPage(t *testing.T, tpl *Template, id int, stage string) {
	t.Helper()
	page := buildAccountPage(id)
	if got := ReplaceMap(tpl, page.values); got != page.want {
		t.Fatalf("%s account %d ReplaceMap = %q want %q", stage, id, got, page.want)
	}
	if got := Replace(tpl, page.pairs); got != page.want {
		t.Fatalf("%s account %d Replace = %q want %q", stage, id, got, page.want)
	}
	if got := ReplaceValues(tpl, page.inOrder); got != page.want {
		t.Fatalf("%s account %d ReplaceValues = %q want %q", stage, id, got, page.want)
	}
}

func TestCacheNeverServesOneAccountPageToAnotherAccount(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, accountPageSource)
	for id := range isolationAccounts {
		for range rendersPerValueSet {
			checkAccountPage(t, tpl, id, "first pass")
		}
	}
	for step := range isolationAccounts {
		checkAccountPage(t, tpl, (step*revisitStride)%isolationAccounts, "revisit")
	}
}

func TestCacheKeepsTemplatesWithIdenticalValuesApart(t *testing.T) {
	e := newTestEngine(t)
	left := mustCompile(t, e, "LEFT:{{v}}")
	right := mustCompile(t, e, "RIGHT:{{v}}")
	for id := range 200 {
		value := "value-" + strconv.Itoa(id)
		for range rendersPerValueSet {
			if got := ReplaceValues(left, []string{value}); got != "LEFT:"+value {
				t.Fatalf("left template rendered %q", got)
			}
			if got := ReplaceValues(right, []string{value}); got != "RIGHT:"+value {
				t.Fatalf("right template rendered %q", got)
			}
			if got := ReplaceMap(left, map[string]string{"v": value}); got != "LEFT:"+value {
				t.Fatalf("left template map rendered %q", got)
			}
			if got := ReplaceMap(right, map[string]string{"v": value}); got != "RIGHT:"+value {
				t.Fatalf("right template map rendered %q", got)
			}
		}
	}
}

func TestCacheKeepsInputFormsApartOnOneTemplate(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "{{a}}|{{b}}")
	twoItems := []string{"a", "b"}
	fourItems := []string{"a", "x", "b", "y"}
	for round := range 5 {
		if got := Replace(tpl, twoItems); got != "b|{{b}}" {
			t.Fatalf("round %d pairs form = %q", round, got)
		}
		if got := ReplaceValues(tpl, twoItems); got != "a|b" {
			t.Fatalf("round %d values form = %q", round, got)
		}
		if got := ReplaceMap(tpl, map[string]string{"a": "b"}); got != "b|{{b}}" {
			t.Fatalf("round %d map form = %q", round, got)
		}
		if got := Replace(tpl, fourItems); got != "x|y" {
			t.Fatalf("round %d four item pairs form = %q", round, got)
		}
		if got := ReplaceValues(tpl, fourItems); got != "a|x" {
			t.Fatalf("round %d four item values form = %q", round, got)
		}
	}
}

func TestCacheSeparatesValueSetsWithShiftedBoundaries(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, boundaryShiftSource)
	check := func(stage string, first int, second int) {
		values := []string{boundaryShiftText[:first], boundaryShiftText[first:second], boundaryShiftText[second:]}
		want := strings.Join(values, "|")
		if got := ReplaceValues(tpl, values); got != want {
			t.Fatalf("%s split %d/%d values form = %q want %q", stage, first, second, got, want)
		}
		pairs := []string{"a", values[0], "b", values[1], "c", values[2]}
		if got := Replace(tpl, pairs); got != want {
			t.Fatalf("%s split %d/%d pairs form = %q want %q", stage, first, second, got, want)
		}
	}
	for _, stage := range []string{"first pass", "revisit"} {
		for first := 0; first <= len(boundaryShiftText); first++ {
			for second := first; second <= len(boundaryShiftText); second++ {
				for range rendersPerValueSet {
					check(stage, first, second)
				}
			}
		}
	}
}

func TestCacheFollowsValuesWhenTheCallerReusesOneContainer(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "{{a}}|{{b}}")
	values := make([]string, 2)
	pairs := []string{"a", "", "b", ""}
	lookup := map[string]string{}
	for round := range reusedSliceRounds {
		first := "f" + strconv.Itoa(round%reusedSliceVariants)
		second := "s" + strconv.Itoa((round/3)%reusedSliceVariants)
		want := first + "|" + second
		values[0], values[1] = first, second
		pairs[1], pairs[3] = first, second
		lookup["a"], lookup["b"] = first, second
		if got := ReplaceValues(tpl, values); got != want {
			t.Fatalf("round %d reused values slice = %q want %q", round, got, want)
		}
		if got := Replace(tpl, pairs); got != want {
			t.Fatalf("round %d reused pairs slice = %q want %q", round, got, want)
		}
		if got := ReplaceMap(tpl, lookup); got != want {
			t.Fatalf("round %d reused map = %q want %q", round, got, want)
		}
	}
}

func TestCacheSeparatesMissingEmptyAndLiteralPlaceholderValues(t *testing.T) {
	const source = "[{{a}}][{{b}}]"
	e := newTestEngine(t)
	tpl := mustCompile(t, e, source)
	cases := []struct {
		name   string
		pairs  []string
		lookup map[string]string
	}{
		{"nothing", nil, nil},
		{"empty containers", []string{}, map[string]string{}},
		{"a empty", []string{"a", ""}, map[string]string{"a": ""}},
		{"b empty", []string{"b", ""}, map[string]string{"b": ""}},
		{"both empty", []string{"a", "", "b", ""}, map[string]string{"a": "", "b": ""}},
		{"a is its own placeholder text", []string{"a", "{{a}}"}, map[string]string{"a": "{{a}}"}},
		{"a is the other placeholder text", []string{"a", "{{b}}"}, map[string]string{"a": "{{b}}"}},
		{"a set", []string{"a", "1"}, map[string]string{"a": "1"}},
		{"b set", []string{"b", "1"}, map[string]string{"b": "1"}},
		{"swapped", []string{"a", "b", "b", "a"}, map[string]string{"a": "b", "b": "a"}},
		{"unknown key only", []string{"z", "1"}, map[string]string{"z": "1"}},
	}
	for round := range 4 {
		for _, tc := range cases {
			want := expectedRender(source, tc.lookup)
			if got := Replace(tpl, tc.pairs); got != want {
				t.Fatalf("round %d %s pairs = %q want %q", round, tc.name, got, want)
			}
			if got := ReplaceMap(tpl, tc.lookup); got != want {
				t.Fatalf("round %d %s map = %q want %q", round, tc.name, got, want)
			}
		}
	}
}

func TestCachedPageIsUntouchedByAppendsIntoCallerBuffers(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, accountPageSource)
	page := buildAccountPage(7)
	for range rendersPerValueSet {
		ReplaceValues(tpl, page.inOrder)
	}
	hit := ReplaceValues(tpl, page.inOrder)
	if again := ReplaceValues(tpl, page.inOrder); !sharesBacking(hit, again) {
		t.Fatal("test setup: expected the warmed page to be served from the cache")
	}

	buf := AppendValues(make([]byte, 0, len(page.want)), tpl, page.inOrder)
	if string(buf) != page.want {
		t.Fatalf("AppendValues = %q want %q", buf, page.want)
	}
	if unsafe.SliceData(buf) == unsafe.StringData(hit) {
		t.Fatal("AppendValues returned the cached page's memory")
	}
	grown := AppendValues(nil, tpl, page.inOrder)
	if unsafe.SliceData(grown) == unsafe.StringData(hit) {
		t.Fatal("AppendValues into nil returned the cached page's memory")
	}
	for i := range buf {
		buf[i] = 'X'
	}
	for i := range grown {
		grown[i] = 'Y'
	}
	if hit != page.want {
		t.Fatalf("cached page changed after caller buffers were overwritten: %q", hit)
	}
	if got := ReplaceValues(tpl, page.inOrder); got != page.want {
		t.Fatalf("cache served %q after caller buffers were overwritten", got)
	}
}

func patternedValue(length int) []byte {
	value := make([]byte, length)
	for i := range value {
		value[i] = byte('a' + (i*31+length*7)%26)
	}
	return value
}

func TestRenderCacheSeparatesValuesInEveryLengthClass(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "<{{v}}>")
	check := func(value string, note string) {
		want := "<" + value + ">"
		for range rendersPerValueSet {
			if got := ReplaceValues(tpl, []string{value}); got != want {
				t.Fatalf("%s values form rendered %q want %q", note, got, want)
			}
			if got := Replace(tpl, []string{value}); got != want {
				t.Fatalf("%s shorthand form rendered %q want %q", note, got, want)
			}
			if got := ReplaceMap(tpl, map[string]string{"v": value}); got != want {
				t.Fatalf("%s map form rendered %q want %q", note, got, want)
			}
		}
	}
	for length := 0; length <= longestValueLength; length++ {
		base := patternedValue(length)
		check(string(base), "length "+strconv.Itoa(length))
		for position := 0; position < length; position++ {
			if position%valueFlipStride != 0 && position != length/2 && position != length-1 {
				continue
			}
			flipped := append([]byte(nil), base...)
			flipped[position] ^= 0x20
			check(string(flipped), "length "+strconv.Itoa(length)+" flipped at "+strconv.Itoa(position))
		}
		extended := append(append([]byte(nil), base...), patternedValue(lengthSecretPeriod)...)
		check(string(extended), "length "+strconv.Itoa(length)+" extended by the length secret period")
	}
}

func TestRenderCacheStopsServingWhenDisabledAndResumesWhenReEnabled(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, accountPageSource)
	page := buildAccountPage(11)
	for range rendersPerValueSet {
		ReplaceValues(tpl, page.inOrder)
	}
	hit := ReplaceValues(tpl, page.inOrder)

	e.SetRenderCacheTTL(RenderCacheDisabled)
	first := ReplaceValues(tpl, page.inOrder)
	second := ReplaceValues(tpl, page.inOrder)
	if first != page.want || second != page.want {
		t.Fatalf("renders with the cache disabled = %q and %q", first, second)
	}
	if sharesBacking(first, hit) || sharesBacking(second, hit) || sharesBacking(first, second) {
		t.Fatal("a cached page was served while the cache was disabled")
	}

	e.SetRenderCacheTTL(0)
	if got := e.RenderCacheTTL(); got != DefaultRenderCacheTTL {
		t.Fatalf("re-enabled TTL = %v want %v", got, DefaultRenderCacheTTL)
	}
	for range rendersPerValueSet {
		if got := ReplaceValues(tpl, page.inOrder); got != page.want {
			t.Fatalf("render after re-enabling = %q", got)
		}
	}
	if a, b := ReplaceValues(tpl, page.inOrder), ReplaceValues(tpl, page.inOrder); !sharesBacking(a, b) {
		t.Fatal("cache did not resume serving after being re-enabled")
	}
}

func TestRenderCacheServesHitsWithTheLargestPossibleTTL(t *testing.T) {
	e := newTestEngine(t, WithRenderCache(largestRenderCacheTTL))
	tpl := mustCompile(t, e, accountPageSource)
	page := buildAccountPage(13)
	for range rendersPerValueSet {
		if got := ReplaceValues(tpl, page.inOrder); got != page.want {
			t.Fatalf("render = %q", got)
		}
	}
	if a, b := ReplaceValues(tpl, page.inOrder), ReplaceValues(tpl, page.inOrder); !sharesBacking(a, b) {
		t.Fatal("a cache configured with the largest TTL never served a hit")
	}
}
