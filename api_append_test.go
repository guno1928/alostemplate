package alos

import (
	"strings"
	"testing"
)

func loadAppendTemplate(t testing.TB, content string) *Template {
	t.Helper()
	engine := New()
	t.Cleanup(engine.Stop)
	tpl, err := engine.Load(writeTemplate(t, t.TempDir(), "page.alos", content))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return tpl
}

func TestAppendReplaceAcceptsEveryInputForm(t *testing.T) {
	tpl := loadAppendTemplate(t, "<b>{{name}}</b>")
	inputs := map[string]any{
		"string shorthand": "alos",
		"pairs":            []string{"name", "alos"},
		"single value":     []string{"alos"},
		"map":              map[string]string{"name": "alos"},
	}
	for name, values := range inputs {
		out, err := AppendReplace([]byte("head:"), tpl, values)
		if err != nil || string(out) != "head:<b>alos</b>" {
			t.Fatalf("%s: got %q, %v", name, out, err)
		}
	}
	out, err := AppendReplace(nil, tpl, nil)
	if err != nil || string(out) != "<b>{{name}}</b>" {
		t.Fatalf("nil values: got %q, %v", out, err)
	}
}

func TestAppendReplaceMatchesReplace(t *testing.T) {
	tpl := loadAppendTemplate(t, "<h1>{{title}}</h1><p>{{body}}</p><i>{{title}}</i>")
	for _, values := range []any{
		[]string{"title", "T", "body", strings.Repeat("b", 500)},
		[]string{"body", "only"},
		map[string]string{"title": "T"},
	} {
		want, err := Replace(tpl, values)
		if err != nil {
			t.Fatalf("Replace: %v", err)
		}
		got, err := AppendReplace(nil, tpl, values)
		if err != nil || string(got) != want {
			t.Fatalf("AppendReplace = %q, %v; want %q", got, err, want)
		}
	}
}

func TestAppendReplaceReportsBadInputAndLeavesDstAlone(t *testing.T) {
	tpl := loadAppendTemplate(t, "{{a}}{{b}}")
	dst := []byte("keep")
	if out, err := AppendReplace(dst, nil, "x"); err == nil || string(out) != "keep" {
		t.Fatalf("nil template: got %q, %v", out, err)
	}
	if out, err := AppendReplace(dst, tpl, []string{"a", "1", "b"}); err == nil || string(out) != "keep" {
		t.Fatalf("odd pairs: got %q, %v", out, err)
	}
	out, err := AppendReplace(dst, tpl, 42)
	if err == nil || string(out) != "keep" || !strings.Contains(err.Error(), "unsupported replacement input int") {
		t.Fatalf("unsupported type: got %q, %v", out, err)
	}
}

func TestPublicRenderCallsDoNotAllocate(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are not meaningful under the race detector")
	}
	tpl := loadAppendTemplate(t, "<h1>{{title}}</h1><p>{{body}}</p>")
	pairs := []string{"title", "Hello", "body", "World"}
	values := map[string]string{"title": "Hello", "body": "World"}
	ordered := []string{"Hello", "World"}
	var rendered string
	var buf []byte
	checks := map[string]func(){
		"ReplaceValues":       func() { rendered = ReplaceValues(tpl, ordered) },
		"AppendValues":        func() { buf = AppendValues(buf[:0], tpl, ordered) },
		"Replace pairs":       func() { rendered, _ = Replace(tpl, pairs) },
		"Replace map":         func() { rendered, _ = Replace(tpl, values) },
		"AppendReplace pairs": func() { buf, _ = AppendReplace(buf[:0], tpl, pairs) },
		"AppendReplace map":   func() { buf, _ = AppendReplace(buf[:0], tpl, values) },
	}
	for name, run := range checks {
		run()
		if allocs := testing.AllocsPerRun(200, run); allocs != 0 {
			t.Fatalf("%s allocates %.1f times per call", name, allocs)
		}
	}
	if rendered != "<h1>Hello</h1><p>World</p>" || string(buf) != rendered {
		t.Fatalf("rendered %q, appended %q", rendered, buf)
	}
}

func TestValuesFillPlaceholdersInKeyOrder(t *testing.T) {
	tpl := loadAppendTemplate(t, "<h1>{{title}}</h1><p>{{body}}</p><i>{{title}}</i>")
	if keys := tpl.Keys(); len(keys) != 2 || keys[0] != "title" || keys[1] != "body" {
		t.Fatalf("Keys() = %q", keys)
	}
	cases := []struct {
		values []string
		want   string
	}{
		{[]string{"T", "B"}, "<h1>T</h1><p>B</p><i>T</i>"},
		{[]string{"T"}, "<h1>T</h1><p>{{body}}</p><i>T</i>"},
		{nil, "<h1>{{title}}</h1><p>{{body}}</p><i>{{title}}</i>"},
		{[]string{"T", "B", "ignored"}, "<h1>T</h1><p>B</p><i>T</i>"},
		{[]string{"", ""}, "<h1></h1><p></p><i></i>"},
	}
	for _, c := range cases {
		for call := 0; call < 3; call++ {
			if got := ReplaceValues(tpl, c.values); got != c.want {
				t.Fatalf("ReplaceValues(%q) call %d = %q; want %q", c.values, call, got, c.want)
			}
		}
		if got := AppendValues([]byte(">"), tpl, c.values); string(got) != ">"+c.want {
			t.Fatalf("AppendValues(%q) = %q", c.values, got)
		}
	}
	pairsOut, _ := Replace(tpl, []string{"title", "T", "body", "B"})
	if valuesOut := ReplaceValues(tpl, []string{"T", "B"}); pairsOut != valuesOut {
		t.Fatalf("pairs %q and values %q disagree", pairsOut, valuesOut)
	}
	if ReplaceValues(nil, []string{"x"}) != "" || string(AppendValues([]byte("keep"), nil, []string{"x"})) != "keep" {
		t.Fatal("a nil template should render nothing")
	}
}
