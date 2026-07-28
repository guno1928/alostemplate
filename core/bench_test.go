package core

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

var benchSink []byte

var benchSlotCounts = []int{0, 1, 4, 8, 16, 64}

func benchName(slots int) string {
	if slots == 0 {
		return "static"
	}
	return "slots" + strconv.Itoa(slots)
}

func benchTemplate(b *testing.B, e *Engine, slots int) (*Template, string) {
	b.Helper()
	src := buildSource(slots, 24, "key")
	tpl, err := e.compileSource(src)
	if err != nil {
		b.Fatalf("compileSource: %v", err)
	}
	return tpl, src
}

func BenchmarkReplacePairsCold(b *testing.B) {
	e := NewEngine()
	defer e.Stop()
	for _, slots := range benchSlotCounts {
		tpl, _ := benchTemplate(b, e, slots)
		pairs := buildPairs(slots, "key", 12)
		b.Run(benchName(slots), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				sinkString = Replace(tpl, pairs)
			}
		})
	}
}

func BenchmarkReplacePairsWarm(b *testing.B) {
	e := NewEngine()
	defer e.Stop()
	for _, slots := range benchSlotCounts {
		tpl, _ := benchTemplate(b, e, slots)
		pairs := buildPairs(slots, "key", 12)
		b.Run(benchName(slots), func(b *testing.B) {
			dst := make([]byte, 0, 1<<16)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sinkString = Replace(tpl, pairs)
			}
			benchSink = dst
		})
	}
}

func BenchmarkReplaceMapCold(b *testing.B) {
	e := NewEngine()
	defer e.Stop()
	for _, slots := range benchSlotCounts {
		tpl, _ := benchTemplate(b, e, slots)
		values := buildValues(slots, "key", 12)
		b.Run(benchName(slots), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				sinkString = ReplaceMap(tpl, values)
			}
		})
	}
}

func BenchmarkReplaceMapWarm(b *testing.B) {
	e := NewEngine()
	defer e.Stop()
	for _, slots := range benchSlotCounts {
		tpl, _ := benchTemplate(b, e, slots)
		values := buildValues(slots, "key", 12)
		b.Run(benchName(slots), func(b *testing.B) {
			dst := make([]byte, 0, 1<<16)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sinkString = ReplaceMap(tpl, values)
			}
			benchSink = dst
		})
	}
}

func BenchmarkReplaceSingleShorthand(b *testing.B) {
	e := NewEngine()
	defer e.Stop()
	tpl, err := e.compileSource("Hello {{name}}, welcome back to the site!")
	if err != nil {
		b.Fatalf("compile: %v", err)
	}
	pairs := []string{"Ada Lovelace"}
	dst := make([]byte, 0, 256)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkString = Replace(tpl, pairs)
	}
	benchSink = dst
}

func BenchmarkReplaceSingleKeyed(b *testing.B) {
	e := NewEngine()
	defer e.Stop()
	tpl, err := e.compileSource("Hello {{name}}, welcome back to the site!")
	if err != nil {
		b.Fatalf("compile: %v", err)
	}
	pairs := []string{"name", "Ada Lovelace"}
	dst := make([]byte, 0, 256)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkString = Replace(tpl, pairs)
	}
	benchSink = dst
}

func BenchmarkReplaceSingleMap(b *testing.B) {
	e := NewEngine()
	defer e.Stop()
	tpl, err := e.compileSource("Hello {{name}}, welcome back to the site!")
	if err != nil {
		b.Fatalf("compile: %v", err)
	}
	values := map[string]string{"name": "Ada Lovelace"}
	dst := make([]byte, 0, 256)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkString = ReplaceMap(tpl, values)
	}
	benchSink = dst
}

func BenchmarkReplaceMissingKeys(b *testing.B) {
	e := NewEngine()
	defer e.Stop()
	for _, slots := range []int{4, 16, 64} {
		tpl, _ := benchTemplate(b, e, slots)
		b.Run(benchName(slots), func(b *testing.B) {
			dst := make([]byte, 0, 1<<16)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sinkString = Replace(tpl, nil)
			}
			benchSink = dst
		})
	}
}

func BenchmarkReplacePartialKeys(b *testing.B) {
	e := NewEngine()
	defer e.Stop()
	for _, slots := range []int{4, 16, 64} {
		tpl, _ := benchTemplate(b, e, slots)
		full := buildValues(slots, "key", 12)
		partial := make(map[string]string, slots/2)
		idx := 0
		for k, v := range full {
			if idx%2 == 0 {
				partial[k] = v
			}
			idx++
		}
		b.Run(benchName(slots), func(b *testing.B) {
			dst := make([]byte, 0, 1<<16)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sinkString = ReplaceMap(tpl, partial)
			}
			benchSink = dst
		})
	}
}

func BenchmarkReplaceDuplicateKeys(b *testing.B) {
	e := NewEngine()
	defer e.Stop()
	src := ""
	for i := 0; i < 32; i++ {
		src += "literal text here {{shared}}"
	}
	tpl, err := e.compileSource(src)
	if err != nil {
		b.Fatalf("compile: %v", err)
	}
	pairs := []string{"shared", "value"}
	dst := make([]byte, 0, 1<<16)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkString = Replace(tpl, pairs)
	}
	benchSink = dst
}

func BenchmarkReplaceLargeValues(b *testing.B) {
	e := NewEngine()
	defer e.Stop()
	tpl, err := e.compileSource("<div>{{body}}</div>")
	if err != nil {
		b.Fatalf("compile: %v", err)
	}
	body := make([]byte, 64*1024)
	for i := range body {
		body[i] = byte('a' + i%26)
	}
	pairs := []string{"body", string(body)}
	dst := make([]byte, 0, 128*1024)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkString = Replace(tpl, pairs)
	}
	benchSink = dst
}

const benchHTMLTemplate = `<!DOCTYPE html>
<html lang="{{lang}}">
<head>
<meta charset="utf-8">
<title>{{title}}</title>
<meta name="description" content="{{description}}">
<link rel="canonical" href="{{canonical}}">
</head>
<body class="{{bodyClass}}">
<header><a href="/" class="brand">{{brand}}</a></header>
<nav>{{nav}}</nav>
<main>
<h1>{{heading}}</h1>
<p class="lede">{{lede}}</p>
<article>{{content}}</article>
<aside>{{sidebar}}</aside>
</main>
<footer>
<span>{{copyright}}</span>
<span>{{buildID}}</span>
</footer>
<script src="{{scriptURL}}"></script>
</body>
</html>`

func benchHTMLValues() map[string]string {
	return map[string]string{
		"lang":        "en",
		"title":       "ALOS Template Benchmark Page",
		"description": "A representative page used to measure template rendering throughput.",
		"canonical":   "https://example.com/benchmark",
		"bodyClass":   "page page--benchmark",
		"brand":       "ALOS",
		"nav":         "<ul><li>Home</li><li>Docs</li><li>Pricing</li></ul>",
		"heading":     "Rendering Benchmarks",
		"lede":        "This page exists purely to exercise the replacement hot path.",
		"content":     "<p>Body paragraph one.</p><p>Body paragraph two.</p>",
		"sidebar":     "<ul><li>Related A</li><li>Related B</li></ul>",
		"copyright":   "(c) 2026 ALOS",
		"buildID":     "build-98f2a1c",
		"scriptURL":   "/static/app.min.js",
	}
}

func benchHTMLPairs() []string {
	values := benchHTMLValues()
	pairs := make([]string, 0, len(values)*2)
	for _, k := range []string{
		"lang", "title", "description", "canonical", "bodyClass", "brand", "nav",
		"heading", "lede", "content", "sidebar", "copyright", "buildID", "scriptURL",
	} {
		pairs = append(pairs, k, values[k])
	}
	return pairs
}

func BenchmarkReplaceRealisticHTMLPairs(b *testing.B) {
	e := NewEngine()
	defer e.Stop()
	tpl, err := e.compileSource(benchHTMLTemplate)
	if err != nil {
		b.Fatalf("compile: %v", err)
	}
	pairs := benchHTMLPairs()
	dst := make([]byte, 0, 4096)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkString = Replace(tpl, pairs)
	}
	benchSink = dst
}

func BenchmarkReplaceRealisticHTMLMap(b *testing.B) {
	e := NewEngine()
	defer e.Stop()
	tpl, err := e.compileSource(benchHTMLTemplate)
	if err != nil {
		b.Fatalf("compile: %v", err)
	}
	values := benchHTMLValues()
	dst := make([]byte, 0, 4096)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkString = ReplaceMap(tpl, values)
	}
	benchSink = dst
}

func BenchmarkReplaceParallelPairs(b *testing.B) {
	e := NewEngine()
	defer e.Stop()
	tpl, err := e.compileSource(benchHTMLTemplate)
	if err != nil {
		b.Fatalf("compile: %v", err)
	}
	pairs := benchHTMLPairs()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		dst := make([]byte, 0, 4096)
		for pb.Next() {
			sinkString = Replace(tpl, pairs)
		}
		benchSink = dst
	})
}

func BenchmarkReplaceParallelMap(b *testing.B) {
	e := NewEngine()
	defer e.Stop()
	tpl, err := e.compileSource(benchHTMLTemplate)
	if err != nil {
		b.Fatalf("compile: %v", err)
	}
	values := benchHTMLValues()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		dst := make([]byte, 0, 4096)
		for pb.Next() {
			sinkString = ReplaceMap(tpl, values)
		}
		benchSink = dst
	})
}

func BenchmarkReplaceParallelLargeSlots(b *testing.B) {
	e := NewEngine()
	defer e.Stop()
	tpl, _ := benchTemplate(b, e, 64)
	pairs := buildPairs(64, "key", 12)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		dst := make([]byte, 0, 1<<16)
		for pb.Next() {
			sinkString = Replace(tpl, pairs)
		}
		benchSink = dst
	})
}

func BenchmarkFindReplacement(b *testing.B) {
	for _, n := range []int{1, 4, 16, 64} {
		pairs := buildPairs(n, "key", 8)
		target := "key" + strconv.Itoa(n-1)
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			b.ReportAllocs()
			var ok bool
			var v string
			for i := 0; i < b.N; i++ {
				v, ok = findReplacement(pairs, target)
			}
			if !ok || v == "" {
				b.Fatal("lookup failed")
			}
		})
	}
}

func BenchmarkCompileSource(b *testing.B) {
	e := NewEngine()
	defer e.Stop()
	for _, slots := range benchSlotCounts {
		src := buildSource(slots, 24, "key")
		b.Run(benchName(slots), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := e.compileSource(src); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkCompileRealisticHTML(b *testing.B) {
	e := NewEngine()
	defer e.Stop()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := e.compileSource(benchHTMLTemplate); err != nil {
			b.Fatal(err)
		}
	}
}

func benchWriteFile(b *testing.B, dir, rel, content string) string {
	b.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		b.Fatal(err)
	}
	return full
}

func BenchmarkLoadFileCached(b *testing.B) {
	dir := b.TempDir()
	path := benchWriteFile(b, dir, "page.alos", benchHTMLTemplate)
	e := NewEngine()
	defer e.Stop()
	if _, err := e.Load(path); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.Load(path); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoadFileCachedParallel(b *testing.B) {
	dir := b.TempDir()
	path := benchWriteFile(b, dir, "page.alos", benchHTMLTemplate)
	e := NewEngine()
	defer e.Stop()
	if _, err := e.Load(path); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := e.Load(path); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkLoadDirCached(b *testing.B) {
	dir := b.TempDir()
	benchWriteFile(b, dir, "index.alos", benchHTMLTemplate)
	benchWriteFile(b, dir, "nav.alos", "<nav>{{brand}}</nav>")
	benchWriteFile(b, dir, "footer.alos", "<footer>{{copyright}}</footer>")
	e := NewEngine()
	defer e.Stop()
	if _, err := e.Load(dir); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.Load(dir); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNamedLookup(b *testing.B) {
	dir := b.TempDir()
	benchWriteFile(b, dir, "index.alos", "I")
	benchWriteFile(b, dir, "nav.alos", "N")
	benchWriteFile(b, dir, "footer.alos", "F")
	e := NewEngine()
	defer e.Stop()
	bundle, err := e.Load(dir)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if bundle.Named("nav") == nil {
			b.Fatal("missing")
		}
	}
}

func BenchmarkScanTemplateDirectory(b *testing.B) {
	dir := b.TempDir()
	for i := 0; i < 24; i++ {
		benchWriteFile(b, dir, "page"+strconv.Itoa(i)+".alos", benchHTMLTemplate)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := scanTemplateDirectory(dir); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNormalizeTemplateName(b *testing.B) {
	b.ReportAllocs()
	var sink string
	for i := 0; i < b.N; i++ {
		sink = normalizeTemplateName("Sub/Dir/Page.ALOS")
	}
	if sink == "" {
		b.Fatal("empty")
	}
}
