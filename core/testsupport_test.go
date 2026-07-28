package core

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func writeFile(t testing.TB, dir string, rel string, content string) string {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
	return full
}

func newTestEngine(t testing.TB, opts ...EngineOption) *Engine {
	t.Helper()
	e := NewEngine(opts...)
	t.Cleanup(e.Stop)
	return e
}

func mustCompile(t testing.TB, e *Engine, src string) *Template {
	t.Helper()
	tpl, err := e.compileSource(src)
	if err != nil {
		t.Fatalf("compileSource(%q): %v", src, err)
	}
	return tpl
}

func buildSource(slots int, literalLen int, keyPrefix string) string {
	var b strings.Builder
	literal := strings.Repeat("x", literalLen)
	for i := 0; i < slots; i++ {
		b.WriteString(literal)
		b.WriteString("{{")
		b.WriteString(keyPrefix)
		b.WriteString(strconv.Itoa(i))
		b.WriteString("}}")
	}
	b.WriteString(literal)
	return b.String()
}

func buildPairs(slots int, keyPrefix string, valueLen int) []string {
	pairs := make([]string, 0, slots*2)
	value := strings.Repeat("v", valueLen)
	for i := 0; i < slots; i++ {
		pairs = append(pairs, keyPrefix+strconv.Itoa(i), value)
	}
	return pairs
}

func buildValues(slots int, keyPrefix string, valueLen int) map[string]string {
	values := make(map[string]string, slots)
	value := strings.Repeat("v", valueLen)
	for i := 0; i < slots; i++ {
		values[keyPrefix+strconv.Itoa(i)] = value
	}
	return values
}

func expectedRender(src string, values map[string]string) string {
	var out strings.Builder
	start := 0
	for {
		open := strings.Index(src[start:], "{{")
		if open < 0 {
			out.WriteString(src[start:])
			return out.String()
		}
		open += start
		closeAt := strings.Index(src[open+2:], "}}")
		if closeAt < 0 {
			out.WriteString(src[start:])
			return out.String()
		}
		closeAt += open + 2
		out.WriteString(src[start:open])
		key := strings.TrimSpace(src[open+2 : closeAt])
		if v, ok := values[key]; ok {
			out.WriteString(v)
		} else {
			out.WriteString(src[open : closeAt+2])
		}
		start = closeAt + 2
	}
}
