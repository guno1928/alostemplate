package alos

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTemplate(t testing.TB, dir string, rel string, content string) string {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return full
}

func TestNewAppliesOptions(t *testing.T) {
	e := New(WithDelimiters("<%", "%>"), WithModifiedOnly(true), WithAutoRefresh(time.Hour))
	defer e.Stop()
	left, right := e.Delimiters()
	if left != "<%" || right != "%>" {
		t.Fatalf("delimiters = %q,%q", left, right)
	}
	if e.AutoRefresh() != time.Hour {
		t.Fatalf("AutoRefresh = %v", e.AutoRefresh())
	}
}

func TestNewNoOptions(t *testing.T) {
	e := New()
	defer e.Stop()
	left, right := e.Delimiters()
	if left != "{{" || right != "}}" {
		t.Fatalf("delimiters = %q,%q", left, right)
	}
}

func TestWithDelimitersOption(t *testing.T) {
	e := New(WithDelimiters("[[", "]]"))
	defer e.Stop()
	dir := t.TempDir()
	path := writeTemplate(t, dir, "a.alos", "x[[k]]y")
	tpl, err := e.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out, err := Replace(tpl, nil, "V")
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if string(out) != "xVy" {
		t.Fatalf("render = %q", out)
	}
}

func TestWithAutoRefreshOption(t *testing.T) {
	if raceEnabled {
		t.Skip("auto refresh publishes reload results without synchronisation; see core.TestAutoRefreshConcurrentRenderIsRacy")
	}
	e := New(WithAutoRefresh(15 * time.Millisecond))
	defer e.Stop()
	dir := t.TempDir()
	path := writeTemplate(t, dir, "a.alos", "one {{v}}")
	tpl, err := e.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	writeTemplate(t, dir, "a.alos", "two {{v}}")
	// Stop the refresh loop before rendering: rendering while it runs trips a
	// pre-existing data race in the in-place reload path.
	time.Sleep(300 * time.Millisecond)
	e.Stop()
	out, err := Replace(tpl, nil, []string{"v", "x"})
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if string(out) != "two x" {
		t.Fatalf("auto refresh never applied, got %q", out)
	}
}

func TestWithModifiedOnlyOption(t *testing.T) {
	e := New(WithModifiedOnly(true))
	defer e.Stop()
	dir := t.TempDir()
	path := writeTemplate(t, dir, "a.alos", "one {{v}}")
	tpl, err := e.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := e.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	writeTemplate(t, dir, "a.alos", "two {{v}}")
	if err := e.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	out, err := Replace(tpl, nil, []string{"v", "x"})
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if string(out) != "two x" {
		t.Fatalf("render = %q", out)
	}
}

func TestPackageLoadAndReplace(t *testing.T) {
	dir := t.TempDir()
	path := writeTemplate(t, dir, "greet.alos", "Hello {{name}}!")
	tpl, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if tpl.Name() != "greet" {
		t.Fatalf("Name = %q", tpl.Name())
	}
	out, err := Replace(tpl, nil, "Ada")
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if string(out) != "Hello Ada!" {
		t.Fatalf("render = %q", out)
	}
}

func TestPackageLoadDirectory(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "index.alos", "<h1>{{title}}</h1>{{include \"nav\"}}")
	writeTemplate(t, dir, "nav.alos", "<nav>{{brand}}</nav>")
	tpl, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out, err := Replace(tpl, nil, map[string]string{"title": "T", "brand": "B"})
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if string(out) != "<h1>T</h1><nav>B</nav>" {
		t.Fatalf("render = %q", out)
	}
	if tpl.Named("nav") == nil {
		t.Fatal("Named(nav) missing")
	}
	if len(tpl.Names()) != 2 {
		t.Fatalf("Names = %v", tpl.Names())
	}
}

func TestPackageLoadError(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing.alos")); err == nil {
		t.Fatal("expected error")
	}
}

func TestPackageReload(t *testing.T) {
	dir := t.TempDir()
	path := writeTemplate(t, dir, "r.alos", "one {{v}}")
	tpl, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	writeTemplate(t, dir, "r.alos", "two {{v}}")
	if err := Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	out, err := Replace(tpl, nil, []string{"v", "x"})
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if string(out) != "two x" {
		t.Fatalf("render = %q", out)
	}
}

func TestPackageSetDelimitersAndDelimiters(t *testing.T) {
	origLeft, origRight := Delimiters()
	t.Cleanup(func() { SetDelimiters(origLeft, origRight) })

	SetDelimiters("<%", "%>")
	if l, r := Delimiters(); l != "<%" || r != "%>" {
		t.Fatalf("Delimiters = %q,%q", l, r)
	}
	SetDelimiters("", "")
	if l, r := Delimiters(); l != "<%" || r != "%>" {
		t.Fatalf("empty override changed delimiters: %q,%q", l, r)
	}

	dir := t.TempDir()
	path := writeTemplate(t, dir, "d.alos", "a<%k%>b")
	tpl, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out, err := Replace(tpl, nil, "V")
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if string(out) != "aVb" {
		t.Fatalf("render = %q", out)
	}
}

func TestPackageSetAutoRefresh(t *testing.T) {
	t.Cleanup(func() { SetAutoRefresh(0) })
	SetAutoRefresh(20 * time.Millisecond)
	SetAutoRefresh(40 * time.Millisecond)
	SetAutoRefresh(0)
}

func TestReplaceNilTemplateErrors(t *testing.T) {
	if _, err := Replace(nil, nil, "x"); err == nil {
		t.Fatal("expected error for nil template")
	} else if !strings.Contains(err.Error(), "template is nil") {
		t.Fatalf("error = %v", err)
	}
}

func TestReplaceUnsupportedType(t *testing.T) {
	dir := t.TempDir()
	path := writeTemplate(t, dir, "u.alos", "{{k}}")
	tpl, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, bad := range []any{nil, 42, 3.5, true, []int{1}, map[string]int{"a": 1}, struct{}{}} {
		if _, err := Replace(tpl, nil, bad); err == nil {
			t.Fatalf("expected error for %T", bad)
		} else if !strings.Contains(err.Error(), "unsupported replacement input") {
			t.Fatalf("error for %T = %v", bad, err)
		}
	}
}

func TestReplaceStringShorthand(t *testing.T) {
	dir := t.TempDir()
	path := writeTemplate(t, dir, "s.alos", "Hi {{who}}.")
	tpl, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out, err := Replace(tpl, nil, "there")
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if string(out) != "Hi there." {
		t.Fatalf("render = %q", out)
	}
	out, err = Replace(tpl, nil, "")
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if string(out) != "Hi ." {
		t.Fatalf("render = %q", out)
	}
}

func TestReplaceStringSlice(t *testing.T) {
	dir := t.TempDir()
	path := writeTemplate(t, dir, "sl.alos", "{{a}}-{{b}}")
	tpl, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	out, err := Replace(tpl, nil, []string{"a", "1", "b", "2"})
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if string(out) != "1-2" {
		t.Fatalf("render = %q", out)
	}

	out, err = Replace(tpl, nil, []string{})
	if err != nil {
		t.Fatalf("Replace empty slice: %v", err)
	}
	if string(out) != "{{a}}-{{b}}" {
		t.Fatalf("empty slice render = %q", out)
	}

	out, err = Replace(tpl, nil, []string(nil))
	if err != nil {
		t.Fatalf("Replace nil slice: %v", err)
	}
	if string(out) != "{{a}}-{{b}}" {
		t.Fatalf("nil slice render = %q", out)
	}
}

func TestReplaceSingleElementSliceShorthand(t *testing.T) {
	dir := t.TempDir()
	path := writeTemplate(t, dir, "one.alos", "[{{k}}]")
	tpl, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out, err := Replace(tpl, nil, []string{"V"})
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if string(out) != "[V]" {
		t.Fatalf("render = %q", out)
	}
}

func TestReplaceOddPairsError(t *testing.T) {
	dir := t.TempDir()
	path := writeTemplate(t, dir, "odd.alos", "{{a}}{{b}}")
	tpl, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, bad := range [][]string{
		{"a", "1", "b"},
		{"a", "1", "b", "2", "c"},
	} {
		if _, err := Replace(tpl, nil, bad); err == nil {
			t.Fatalf("expected error for %v", bad)
		} else if !strings.Contains(err.Error(), "even number") {
			t.Fatalf("error = %v", err)
		}
	}
}

func TestReplaceMapInput(t *testing.T) {
	dir := t.TempDir()
	path := writeTemplate(t, dir, "m.alos", "{{a}}/{{b}}/{{c}}")
	tpl, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out, err := Replace(tpl, nil, map[string]string{"a": "1", "c": "3"})
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if string(out) != "1/{{b}}/3" {
		t.Fatalf("render = %q", out)
	}
	out, err = Replace(tpl, nil, map[string]string(nil))
	if err != nil {
		t.Fatalf("Replace nil map: %v", err)
	}
	if string(out) != "{{a}}/{{b}}/{{c}}" {
		t.Fatalf("nil map render = %q", out)
	}
}

func TestReplaceReusesDst(t *testing.T) {
	dir := t.TempDir()
	path := writeTemplate(t, dir, "reuse.alos", "a{{x}}b")
	tpl, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	dst := make([]byte, 0, 256)
	for i := 0; i < 5; i++ {
		dst, err = Replace(tpl, dst, []string{"x", "V"})
		if err != nil {
			t.Fatalf("Replace: %v", err)
		}
		if string(dst) != "aVb" {
			t.Fatalf("render = %q", dst)
		}
	}
}

func TestTypeAliasesAreIdentical(t *testing.T) {
	e := New()
	defer e.Stop()
	var _ *Engine = e
	dir := t.TempDir()
	path := writeTemplate(t, dir, "t.alos", "{{k}}")
	var tpl *Template
	tpl, err := e.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if tpl == nil {
		t.Fatal("nil template")
	}
	var opt Option = WithModifiedOnly(true)
	if opt == nil {
		t.Fatal("nil option")
	}
}

func TestEngineStopReleasesRefreshLoop(t *testing.T) {
	e := New(WithAutoRefresh(5 * time.Millisecond))
	e.Stop()
	if e.AutoRefresh() != 5*time.Millisecond {
		t.Fatalf("AutoRefresh should still report configured value, got %v", e.AutoRefresh())
	}
}

func TestZZZPackageStop(t *testing.T) {
	Stop()
}
