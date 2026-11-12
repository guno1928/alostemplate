package compare

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	alos "github.com/guno1928/alostemplate"
	"github.com/guno1928/alostemplate/compare/qt"
	"github.com/valyala/fasttemplate"
)

var (
	sinkString string
	sinkBytes  []byte
)

type engines struct {
	scenario     Scenario
	pairs        []string
	values       []string
	stringMap    map[string]string
	anyMap       map[string]interface{}
	alosUncached *alos.Template
	alosCached   *alos.Template
	fast         *fasttemplate.Template
	std          *template.Template
	quick        qt.Renderer
}

func loadAlos(tb testing.TB, s Scenario, opts ...alos.Option) *alos.Template {
	tb.Helper()
	path := filepath.Join(tb.TempDir(), strings.ToLower(s.Name)+".alos")
	if err := os.WriteFile(path, []byte(s.BraceSource()), 0o644); err != nil {
		tb.Fatal(err)
	}
	engine := alos.New(opts...)
	tb.Cleanup(engine.Stop)
	tpl, err := engine.Load(path)
	if err != nil {
		tb.Fatal(err)
	}
	return tpl
}

func setup(tb testing.TB, s Scenario) *engines {
	tb.Helper()
	return &engines{
		scenario:     s,
		pairs:        s.Pairs(),
		values:       s.Values(),
		stringMap:    s.Map(),
		anyMap:       s.AnyMap(),
		alosUncached: loadAlos(tb, s, alos.WithRenderCache(alos.RenderCacheDisabled)),
		alosCached:   loadAlos(tb, s),
		fast:         fasttemplate.New(s.BraceSource(), "{{", "}}"),
		std:          template.Must(template.New(s.Name).Parse(s.GoTemplateSource())),
		quick:        qt.Renderers[s.Name],
	}
}

func (e *engines) stdString() string {
	var b strings.Builder
	if err := e.std.Execute(&b, e.stringMap); err != nil {
		panic(err)
	}
	return b.String()
}

type stringRenderer struct {
	name   string
	render func(e *engines) string
}

var stringRenderers = []stringRenderer{
	{"alos_values", func(e *engines) string { return alos.ReplaceValues(e.alosUncached, e.values) }},
	{"alos_values_cached", func(e *engines) string { return alos.ReplaceValues(e.alosCached, e.values) }},
	{"alos_pairs", func(e *engines) string { out, _ := alos.Replace(e.alosUncached, e.pairs); return out }},
	{"alos_map", func(e *engines) string { out, _ := alos.Replace(e.alosUncached, e.stringMap); return out }},
	{"alos_pairs_cached", func(e *engines) string { out, _ := alos.Replace(e.alosCached, e.pairs); return out }},
	{"alos_map_cached", func(e *engines) string { out, _ := alos.Replace(e.alosCached, e.stringMap); return out }},
	{"quicktemplate", func(e *engines) string { return e.quick.String(e.values) }},
	{"fasttemplate", func(e *engines) string { return e.fast.ExecuteString(e.anyMap) }},
	{"text_template", func(e *engines) string { return e.stdString() }},
}

func TestAllEnginesProduceIdenticalOutput(t *testing.T) {
	for _, s := range Scenarios {
		e := setup(t, s)
		want := s.Expected()
		for _, r := range stringRenderers {
			if got := r.render(e); got != want {
				t.Fatalf("%s/%s: output differs (len %d want %d)", s.Name, r.name, len(got), len(want))
			}
		}
		var buf bytes.Buffer
		e.quick.Write(&buf, e.values)
		if buf.String() != want {
			t.Fatalf("%s/quicktemplate writer output differs", s.Name)
		}
		buf.Reset()
		if _, err := e.fast.Execute(&buf, e.anyMap); err != nil || buf.String() != want {
			t.Fatalf("%s/fasttemplate writer output differs: %v", s.Name, err)
		}
		if got := alos.AppendValues([]byte("prefix:"), e.alosUncached, e.values); string(got) != "prefix:"+want {
			t.Fatalf("%s/alos AppendValues output differs", s.Name)
		}
		for _, values := range []any{e.pairs, e.stringMap} {
			appended, err := alos.AppendReplace([]byte("prefix:"), e.alosUncached, values)
			if err != nil || string(appended) != "prefix:"+want {
				t.Fatalf("%s/alos append output differs: %v", s.Name, err)
			}
			appended, err = alos.AppendReplace(appended[:0], e.alosUncached, values)
			if err != nil || string(appended) != want {
				t.Fatalf("%s/alos append into reused buffer differs: %v", s.Name, err)
			}
		}
	}
}

func BenchmarkString(b *testing.B) {
	for _, s := range Scenarios {
		for _, r := range stringRenderers {
			b.Run(s.Name+"/"+r.name, func(b *testing.B) {
				e := setup(b, s)
				b.ReportAllocs()
				b.SetBytes(int64(len(s.Expected())))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					sinkString = r.render(e)
				}
			})
		}
	}
}

func BenchmarkStringParallel(b *testing.B) {
	for _, s := range Scenarios {
		for _, r := range stringRenderers {
			b.Run(s.Name+"/"+r.name, func(b *testing.B) {
				e := setup(b, s)
				b.ReportAllocs()
				b.SetBytes(int64(len(s.Expected())))
				b.ResetTimer()
				b.RunParallel(func(pb *testing.PB) {
					var local string
					for pb.Next() {
						local = r.render(e)
					}
					_ = local
				})
			})
		}
	}
}

type bufferRenderer struct {
	name   string
	render func(e *engines, buf *bytes.Buffer, raw []byte) []byte
}

var bufferRenderers = []bufferRenderer{
	{"alos_values", func(e *engines, _ *bytes.Buffer, raw []byte) []byte {
		return alos.AppendValues(raw[:0], e.alosUncached, e.values)
	}},
	{"alos_pairs", func(e *engines, _ *bytes.Buffer, raw []byte) []byte {
		raw, _ = alos.AppendReplace(raw[:0], e.alosUncached, e.pairs)
		return raw
	}},
	{"alos_map", func(e *engines, _ *bytes.Buffer, raw []byte) []byte {
		raw, _ = alos.AppendReplace(raw[:0], e.alosUncached, e.stringMap)
		return raw
	}},
	{"quicktemplate", func(e *engines, buf *bytes.Buffer, raw []byte) []byte {
		buf.Reset()
		e.quick.Write(buf, e.values)
		return raw
	}},
	{"fasttemplate", func(e *engines, buf *bytes.Buffer, raw []byte) []byte {
		buf.Reset()
		if _, err := e.fast.Execute(buf, e.anyMap); err != nil {
			panic(err)
		}
		return raw
	}},
	{"text_template", func(e *engines, buf *bytes.Buffer, raw []byte) []byte {
		buf.Reset()
		if err := e.std.Execute(buf, e.stringMap); err != nil {
			panic(err)
		}
		return raw
	}},
}

func BenchmarkBuffer(b *testing.B) {
	for _, s := range Scenarios {
		for _, r := range bufferRenderers {
			b.Run(s.Name+"/"+r.name, func(b *testing.B) {
				e := setup(b, s)
				var buf bytes.Buffer
				var raw []byte
				raw = r.render(e, &buf, raw)
				b.ReportAllocs()
				b.SetBytes(int64(len(s.Expected())))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					raw = r.render(e, &buf, raw)
				}
				sinkBytes = raw
			})
		}
	}
}
