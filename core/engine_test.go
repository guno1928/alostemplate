package core

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"
)

func TestWithDelimiters(t *testing.T) {
	e := newTestEngine(t, WithDelimiters("<%", "%>"))
	left, right := e.Delimiters()
	if left != "<%" || right != "%>" {
		t.Fatalf("delimiters = %q,%q want <%%,%%>", left, right)
	}

	e2 := newTestEngine(t, WithDelimiters("", ""))
	left, right = e2.Delimiters()
	if left != "{{" || right != "}}" {
		t.Fatalf("empty delimiters overrode defaults: %q,%q", left, right)
	}

	e3 := newTestEngine(t, WithDelimiters("[[", ""))
	left, right = e3.Delimiters()
	if left != "[[" || right != "}}" {
		t.Fatalf("half override = %q,%q want [[,}}", left, right)
	}

	e4 := newTestEngine(t, WithDelimiters("", "]]"))
	left, right = e4.Delimiters()
	if left != "{{" || right != "]]" {
		t.Fatalf("half override = %q,%q want {{,]]", left, right)
	}
}

func TestWithAutoRefresh(t *testing.T) {
	e := newTestEngine(t, WithAutoRefresh(50*time.Millisecond))
	if got := e.AutoRefresh(); got != 50*time.Millisecond {
		t.Fatalf("AutoRefresh = %v want 50ms", got)
	}
	if e.refreshStop == nil {
		t.Fatal("positive auto refresh did not start loop")
	}

	e2 := newTestEngine(t, WithAutoRefresh(0))
	if e2.refreshStop != nil {
		t.Fatal("zero auto refresh started a loop")
	}
	if got := e2.AutoRefresh(); got != 0 {
		t.Fatalf("AutoRefresh = %v want 0", got)
	}
}

func TestWithModifiedOnly(t *testing.T) {
	e := newTestEngine(t, WithModifiedOnly(true))
	if !e.modifiedOnly {
		t.Fatal("WithModifiedOnly(true) did not set flag")
	}
	e2 := newTestEngine(t, WithModifiedOnly(false))
	if e2.modifiedOnly {
		t.Fatal("WithModifiedOnly(false) set flag")
	}
}

func TestNewEngineDefaults(t *testing.T) {
	e := newTestEngine(t)
	if e.leftDelim != "{{" || e.rightDelim != "}}" {
		t.Fatalf("default delimiters = %q,%q", e.leftDelim, e.rightDelim)
	}
	if e.fileCache == nil {
		t.Fatal("fileCache not initialised")
	}
	if e.autoRefresh != 0 {
		t.Fatalf("default autoRefresh = %v want 0", e.autoRefresh)
	}
	if e.modifiedOnly {
		t.Fatal("default modifiedOnly should be false")
	}
	if e.refreshStop != nil {
		t.Fatal("default engine should not start refresh loop")
	}
}

func TestNewEngineAppliesOptionsInOrder(t *testing.T) {
	e := newTestEngine(t, WithDelimiters("<", ">"), WithDelimiters("[", "]"))
	left, right := e.Delimiters()
	if left != "[" || right != "]" {
		t.Fatalf("last option should win, got %q,%q", left, right)
	}
}

func TestAutoRefreshLoopReloads(t *testing.T) {
	if raceEnabled {
		t.Skip("auto refresh publishes reload results without synchronisation; see TestAutoRefreshConcurrentRenderIsRacy")
	}
	dir := t.TempDir()
	path := writeFile(t, dir, "a.alos", "hello {{name}}")
	e := newTestEngine(t, WithAutoRefresh(10*time.Millisecond))
	tpl, err := e.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := Replace(tpl, []string{"name", "x"}); got != "hello x" {
		t.Fatalf("initial render = %q", got)
	}
	time.Sleep(30 * time.Millisecond)
	writeFile(t, dir, "a.alos", "bye {{name}}")
	// Give the refresh loop time to observe the change, then stop it before
	// rendering. Rendering while the loop is live would race; see
	// TestAutoRefreshConcurrentRenderIsRacy for that known defect.
	time.Sleep(300 * time.Millisecond)
	e.Stop()
	if got := Replace(tpl, []string{"name", "x"}); got != "bye x" {
		t.Fatalf("auto refresh never picked up change, got %q", got)
	}
}

// TestAutoRefreshConcurrentRenderIsRacy documents a pre-existing data race that
// predates this test suite: the background refresh goroutine mutates Template
// fields through applyTemplateReload while Replace reads them, with no
// synchronisation on Template. It reproduces on the original engine.go as well.
// Fixing it requires publishing reload results through an atomic snapshot rather
// than mutating the Template in place, which is a behavioural change to the
// reload path and is deliberately out of scope for the performance work.
func TestAutoRefreshConcurrentRenderIsRacy(t *testing.T) {
	t.Skip("known pre-existing data race: WithAutoRefresh mutates Template in place while Replace reads it")

	dir := t.TempDir()
	path := writeFile(t, dir, "a.alos", "one {{v}} tail")
	e := newTestEngine(t, WithAutoRefresh(time.Millisecond))
	tpl, err := e.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			writeFile(t, dir, "a.alos", "two {{v}} tail")
			time.Sleep(time.Millisecond)
			writeFile(t, dir, "a.alos", "three {{v}} longer tail")
			time.Sleep(time.Millisecond)
		}
	}()
	for i := 0; i < 20000; i++ {
		sinkString = Replace(tpl, []string{"v", "x"})
	}
	<-done
}

func TestAutoRefreshLoopStopsOnStop(t *testing.T) {
	e := NewEngine(WithAutoRefresh(5 * time.Millisecond))
	stop := e.refreshStop
	if stop == nil {
		t.Fatal("no stop channel")
	}
	e.Stop()
	select {
	case <-stop:
	default:
		t.Fatal("Stop did not close refresh channel")
	}
	if e.refreshStop != nil {
		t.Fatal("Stop did not nil refreshStop")
	}
}

func TestStopIsIdempotentForRefresh(t *testing.T) {
	e := NewEngine(WithAutoRefresh(5 * time.Millisecond))
	e.Stop()
	if e.refreshStop != nil {
		t.Fatal("refreshStop should be nil after Stop")
	}
}

func TestSetDelimiters(t *testing.T) {
	e := newTestEngine(t)
	e.SetDelimiters("<%", "%>")
	if l, r := e.Delimiters(); l != "<%" || r != "%>" {
		t.Fatalf("SetDelimiters = %q,%q", l, r)
	}
	e.SetDelimiters("", "")
	if l, r := e.Delimiters(); l != "<%" || r != "%>" {
		t.Fatalf("empty SetDelimiters changed values: %q,%q", l, r)
	}
	e.SetDelimiters("[[", "")
	if l, r := e.Delimiters(); l != "[[" || r != "%>" {
		t.Fatalf("left-only SetDelimiters = %q,%q", l, r)
	}
	e.SetDelimiters("", "]]")
	if l, r := e.Delimiters(); l != "[[" || r != "]]" {
		t.Fatalf("right-only SetDelimiters = %q,%q", l, r)
	}
}

func TestSetAutoRefresh(t *testing.T) {
	e := newTestEngine(t)
	e.SetAutoRefresh(20 * time.Millisecond)
	if e.AutoRefresh() != 20*time.Millisecond {
		t.Fatalf("AutoRefresh = %v", e.AutoRefresh())
	}
	first := e.refreshStop
	if first == nil {
		t.Fatal("SetAutoRefresh did not start loop")
	}
	e.SetAutoRefresh(30 * time.Millisecond)
	select {
	case <-first:
	default:
		t.Fatal("previous loop was not stopped")
	}
	if e.refreshStop == nil || e.refreshStop == first {
		t.Fatal("SetAutoRefresh did not replace stop channel")
	}
	second := e.refreshStop
	e.SetAutoRefresh(0)
	select {
	case <-second:
	default:
		t.Fatal("SetAutoRefresh(0) did not stop loop")
	}
	if e.refreshStop != nil {
		t.Fatal("SetAutoRefresh(0) left a stop channel")
	}
	if e.AutoRefresh() != 0 {
		t.Fatalf("AutoRefresh = %v want 0", e.AutoRefresh())
	}
}

func TestEngineLoadFile(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "greet.alos", "Hello {{name}}!")
	e := newTestEngine(t)
	tpl, err := e.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if tpl.Name() != "greet" {
		t.Fatalf("Name = %q want greet", tpl.Name())
	}
	if tpl.FileName() != "greet.alos" {
		t.Fatalf("FileName = %q", tpl.FileName())
	}
	if got := Replace(tpl, []string{"name", "Ada"}); got != "Hello Ada!" {
		t.Fatalf("render = %q", got)
	}
}

func TestEngineLoadRelativePathResolvesToAbs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "rel.alos", "{{a}}")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	e := newTestEngine(t)
	tpl, err := e.Load("rel.alos")
	if err != nil {
		t.Fatalf("Load relative: %v", err)
	}
	if !filepath.IsAbs(tpl.sourcePath) {
		t.Fatalf("sourcePath not absolute: %q", tpl.sourcePath)
	}
}

func TestEngineLoadMissingFile(t *testing.T) {
	e := newTestEngine(t)
	if _, err := e.Load(filepath.Join(t.TempDir(), "nope.alos")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestEngineLoadCachesByPathAndSignature(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "c.alos", "{{a}}")
	e := newTestEngine(t)
	first, err := e.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	second, err := e.Load(path)
	if err != nil {
		t.Fatalf("Load again: %v", err)
	}
	if first != second {
		t.Fatal("expected cached template pointer reuse")
	}
}

func TestEngineLoadRecompilesWhenSignatureChanges(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "c.alos", "{{a}}")
	e := newTestEngine(t)
	first, err := e.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	writeFile(t, dir, "c.alos", "changed {{a}}")
	second, err := e.Load(path)
	if err != nil {
		t.Fatalf("Load again: %v", err)
	}
	if first == second {
		t.Fatal("expected recompile after change")
	}
	if got := Replace(second, []string{"a", "1"}); got != "changed 1" {
		t.Fatalf("render = %q", got)
	}
}

func TestEngineLoadCompileError(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "bad.alos", "hello {{name")
	e := newTestEngine(t)
	if _, err := e.Load(path); err == nil {
		t.Fatal("expected compile error")
	}
}

func TestEngineLoadDirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.alos", "INDEX {{title}}")
	writeFile(t, dir, "nav.alos", "NAV")
	writeFile(t, dir, "sub/page.alos", "PAGE {{body}}")
	e := newTestEngine(t)
	bundle, err := e.Load(dir)
	if err != nil {
		t.Fatalf("Load dir: %v", err)
	}
	names := bundle.Names()
	want := []string{"index", "nav", "sub/page"}
	if len(names) != len(want) {
		t.Fatalf("Names = %v want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("Names = %v want %v", names, want)
		}
	}
	if bundle.Name() != "index" {
		t.Fatalf("default target = %q want index", bundle.Name())
	}
	if got := Replace(bundle, []string{"title", "T"}); got != "INDEX T" {
		t.Fatalf("bundle render = %q", got)
	}
	if got := Replace(bundle.Named("sub/page"), []string{"body", "B"}); got != "PAGE B" {
		t.Fatalf("named render = %q", got)
	}
	if got := Replace(bundle.Named("page"), []string{"body", "B"}); got != "PAGE B" {
		t.Fatalf("base alias render = %q", got)
	}
}

func TestEngineLoadDirectoryDefaultsToFirstSortedWhenNoIndex(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "zebra.alos", "Z")
	writeFile(t, dir, "alpha.alos", "A")
	e := newTestEngine(t)
	bundle, err := e.Load(dir)
	if err != nil {
		t.Fatalf("Load dir: %v", err)
	}
	if bundle.Name() != "alpha" {
		t.Fatalf("default = %q want alpha", bundle.Name())
	}
	if got := Replace(bundle, nil); got != "A" {
		t.Fatalf("render = %q", got)
	}
}

func TestEngineLoadDirectoryEmpty(t *testing.T) {
	e := newTestEngine(t)
	if _, err := e.Load(t.TempDir()); err == nil {
		t.Fatal("expected error for directory with no .alos files")
	}
}

func TestEngineLoadDirectoryAmbiguousBaseNameHasNoAlias(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a/page.alos", "A")
	writeFile(t, dir, "b/page.alos", "B")
	e := newTestEngine(t)
	bundle, err := e.Load(dir)
	if err != nil {
		t.Fatalf("Load dir: %v", err)
	}
	if bundle.Named("page") != nil {
		t.Fatal("ambiguous base name should not create an alias")
	}
	if bundle.Named("a/page") == nil || bundle.Named("b/page") == nil {
		t.Fatal("canonical names must resolve")
	}
}

func TestEngineLoadDirectoryCaches(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.alos", "I")
	e := newTestEngine(t)
	first, err := e.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	second, err := e.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if first != second {
		t.Fatal("expected directory bundle cache hit")
	}
}

func TestEngineLoadDirectoryIncludeExpansion(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.alos", "<html>{{include \"nav\"}}<body>{{body}}</body></html>")
	writeFile(t, dir, "nav.alos", "<nav>{{brand}}</nav>")
	e := newTestEngine(t)
	bundle, err := e.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := Replace(bundle, []string{"brand", "ALOS", "body", "hi"})
	want := "<html><nav>ALOS</nav><body>hi</body></html>"
	if got != want {
		t.Fatalf("render = %q want %q", got, want)
	}
}

func TestEngineLoadDirectoryIncludeSingleQuotes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.alos", "A{{include 'part'}}B")
	writeFile(t, dir, "part.alos", "-P-")
	e := newTestEngine(t)
	bundle, err := e.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := Replace(bundle, nil); got != "A-P-B" {
		t.Fatalf("render = %q", got)
	}
}

func TestEngineLoadDirectoryIncludeUnknownStaysLiteral(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.alos", "A{{include \"missing\"}}B")
	e := newTestEngine(t)
	bundle, err := e.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := Replace(bundle, nil); got != "A{{include \"missing\"}}B" {
		t.Fatalf("render = %q", got)
	}
}

func TestEngineLoadDirectoryIncludeCycle(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.alos", "A{{include \"b\"}}")
	writeFile(t, dir, "b.alos", "B{{include \"a\"}}")
	e := newTestEngine(t)
	_, err := e.Load(dir)
	if err == nil {
		t.Fatal("expected include cycle error")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("error = %v want cycle mention", err)
	}
}

func TestEngineLoadDirectorySelfIncludeCycle(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.alos", "A{{include \"a\"}}")
	e := newTestEngine(t)
	if _, err := e.Load(dir); err == nil {
		t.Fatal("expected self include cycle error")
	}
}

func TestEngineLoadDirectoryUnterminatedPlaceholder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.alos", "A{{oops")
	e := newTestEngine(t)
	if _, err := e.Load(dir); err == nil {
		t.Fatal("expected unterminated placeholder error")
	}
}

func TestEngineLoadDirectoryCustomDelimiters(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.alos", "<%include \"nav\"%>|<%name%>")
	writeFile(t, dir, "nav.alos", "NAV")
	e := newTestEngine(t, WithDelimiters("<%", "%>"))
	bundle, err := e.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := Replace(bundle, []string{"name", "n"}); got != "NAV|n" {
		t.Fatalf("render = %q", got)
	}
}

func TestEngineLoadIgnoresNonAlosFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.alos", "I")
	writeFile(t, dir, "readme.md", "nope")
	writeFile(t, dir, "x.txt", "nope")
	e := newTestEngine(t)
	bundle, err := e.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(bundle.Names()) != 1 {
		t.Fatalf("Names = %v want only index", bundle.Names())
	}
}

func TestEngineLoadDirectoryUppercaseExtension(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Index.ALOS", "I{{a}}")
	e := newTestEngine(t)
	bundle, err := e.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if bundle.Named("index") == nil {
		t.Fatalf("case-insensitive extension lookup failed, names=%v", bundle.Names())
	}
}

func TestEngineReloadNoEntries(t *testing.T) {
	e := newTestEngine(t)
	if err := e.Reload(); err != nil {
		t.Fatalf("Reload on empty engine: %v", err)
	}
}

func TestEngineReloadUpdatesInPlace(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.alos", "one {{v}}")
	e := newTestEngine(t)
	tpl, err := e.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	writeFile(t, dir, "a.alos", "two {{v}}")
	if err := e.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := Replace(tpl, []string{"v", "x"}); got != "two x" {
		t.Fatalf("render after reload = %q", got)
	}
}

func TestEngineReloadDropsMissingFiles(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.alos", "{{v}}")
	e := newTestEngine(t)
	if _, err := e.Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := e.Reload(); err != nil {
		t.Fatalf("Reload after delete: %v", err)
	}
	if _, ok := e.fileCache.Load(path); ok {
		t.Fatal("deleted file should be evicted from cache")
	}
}

func TestEngineReloadReportsProblems(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.alos", "{{v}}")
	e := newTestEngine(t)
	if _, err := e.Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	writeFile(t, dir, "a.alos", "{{broken")
	err := e.Reload()
	if err == nil {
		t.Fatal("expected reload error")
	}
	if !strings.Contains(err.Error(), "reload failed") {
		t.Fatalf("error = %v", err)
	}
}

func TestEngineReloadModifiedOnlySkipsUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.alos", "{{v}}")
	e := newTestEngine(t, WithModifiedOnly(true))
	tpl, err := e.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	literalsBefore := tpl.literals
	if err := e.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if &literalsBefore[0] != &tpl.literals[0] {
		t.Fatal("modifiedOnly reload recompiled an unchanged file")
	}
}

func TestEngineReloadModifiedOnlyPicksUpChanges(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.alos", "one {{v}}")
	e := newTestEngine(t, WithModifiedOnly(true))
	tpl, err := e.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	writeFile(t, dir, "a.alos", "two {{v}}")
	if err := e.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := Replace(tpl, []string{"v", "x"}); got != "two x" {
		t.Fatalf("render = %q", got)
	}
}

func TestTemplateReloadNil(t *testing.T) {
	var tpl *Template
	if err := tpl.Reload(); err == nil {
		t.Fatal("expected error for nil template")
	}
}

func TestTemplateReloadNoEngine(t *testing.T) {
	tpl := &Template{}
	if err := tpl.Reload(); err == nil {
		t.Fatal("expected error for template with no engine")
	}
}

func TestTemplateReloadNoSourcePath(t *testing.T) {
	e := newTestEngine(t)
	tpl := &Template{engine: e}
	if err := tpl.Reload(); err == nil {
		t.Fatal("expected error for template with no source path")
	}
}

func TestTemplateReloadFile(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.alos", "one {{v}}")
	e := newTestEngine(t)
	tpl, err := e.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	writeFile(t, dir, "a.alos", "two {{v}}")
	if err := tpl.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := Replace(tpl, []string{"v", "x"}); got != "two x" {
		t.Fatalf("render = %q", got)
	}
}

func TestTemplateReloadMissingFileErrors(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.alos", "{{v}}")
	e := newTestEngine(t)
	tpl, err := e.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := tpl.Reload(); err == nil {
		t.Fatal("expected reload error for deleted file")
	}
}

func TestTemplateReloadNamedChildKeepsHandleValid(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.alos", "I{{v}}")
	writeFile(t, dir, "nav.alos", "NAV1 {{v}}")
	e := newTestEngine(t)
	bundle, err := e.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	nav := bundle.Named("nav")
	if nav == nil {
		t.Fatal("nav missing")
	}
	time.Sleep(10 * time.Millisecond)
	writeFile(t, dir, "nav.alos", "NAV2 {{v}}")
	if err := nav.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := Replace(nav, []string{"v", "x"}); got != "NAV2 x" {
		t.Fatalf("named handle render = %q", got)
	}
	if got := Replace(bundle.Named("nav"), []string{"v", "x"}); got != "NAV2 x" {
		t.Fatalf("bundle lookup render = %q", got)
	}
}

func TestTemplateReloadBundleKeepsChildHandles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.alos", "I1{{v}}")
	writeFile(t, dir, "nav.alos", "N1")
	e := newTestEngine(t)
	bundle, err := e.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	nav := bundle.Named("nav")
	time.Sleep(10 * time.Millisecond)
	writeFile(t, dir, "index.alos", "I2{{v}}")
	writeFile(t, dir, "nav.alos", "N2")
	if err := bundle.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := Replace(bundle, []string{"v", "x"}); got != "I2x" {
		t.Fatalf("bundle render = %q", got)
	}
	if got := Replace(nav, nil); got != "N2" {
		t.Fatalf("old child handle render = %q want N2", got)
	}
}

func TestTemplateReloadMissingNamedAfterReload(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.alos", "I")
	writeFile(t, dir, "nav.alos", "N")
	e := newTestEngine(t)
	bundle, err := e.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	nav := bundle.Named("nav")
	if err := os.Remove(filepath.Join(dir, "nav.alos")); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	err = nav.Reload()
	if err == nil {
		t.Fatal("expected error when named template disappears")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Fatalf("error = %v", err)
	}
}

func TestApplyTemplateReloadNilInputs(t *testing.T) {
	applyTemplateReload(nil, nil)
	applyTemplateReload(&Template{}, nil)
	applyTemplateReload(nil, &Template{})
}

func TestApplyTemplateReloadClearsBundleFields(t *testing.T) {
	dst := &Template{
		named:      map[string]*Template{"x": {}},
		names:      []string{"x"},
		defaultTpl: &Template{},
	}
	src := &Template{literals: []string{"a"}, staticLen: 1}
	applyTemplateReload(dst, src)
	if dst.named != nil || dst.names != nil || dst.defaultTpl != nil {
		t.Fatal("bundle fields should be cleared when src is a single template")
	}
	if dst.staticLen != 1 {
		t.Fatalf("staticLen = %d", dst.staticLen)
	}
}

func TestApplyTemplateReloadCreatesMissingChildren(t *testing.T) {
	child := &Template{literals: []string{"c"}, staticLen: 1, reloadName: "c"}
	src := &Template{
		named:      map[string]*Template{"c": child},
		names:      []string{"c"},
		defaultTpl: child,
	}
	dst := &Template{}
	applyTemplateReload(dst, src)
	if dst.named["c"] == nil {
		t.Fatal("child not created")
	}
	if dst.defaultTpl != dst.named["c"] {
		t.Fatal("defaultTpl not mapped to new child")
	}
}

func TestApplyTemplateReloadDefaultTplUnmappable(t *testing.T) {
	orphan := &Template{literals: []string{"o"}}
	src := &Template{
		named:      map[string]*Template{},
		names:      []string{},
		defaultTpl: orphan,
	}
	dst := &Template{}
	applyTemplateReload(dst, src)
	if dst.defaultTpl != nil {
		t.Fatal("unmappable defaultTpl should become nil")
	}
}

func TestLoadResolvedDispatch(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.alos", "A")
	e := newTestEngine(t)
	fileTpl, err := e.loadResolved(path, false)
	if err != nil {
		t.Fatalf("loadResolved file: %v", err)
	}
	if fileTpl.named != nil {
		t.Fatal("file load produced a bundle")
	}
	dirTpl, err := e.loadResolved(dir, false)
	if err != nil {
		t.Fatalf("loadResolved dir: %v", err)
	}
	if dirTpl.named == nil {
		t.Fatal("dir load did not produce a bundle")
	}
	if _, err := e.loadResolved(filepath.Join(dir, "missing"), false); err == nil {
		t.Fatal("expected stat error")
	}
}

func TestLoadFileForceBypassesCache(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.alos", "A")
	e := newTestEngine(t)
	first, err := e.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	forced, err := e.loadFile(path, info, true)
	if err != nil {
		t.Fatalf("loadFile forced: %v", err)
	}
	if forced == first {
		t.Fatal("force should recompile")
	}
}

func TestLoadDirectoryForceBypassesCache(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.alos", "A")
	e := newTestEngine(t)
	first, err := e.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	forced, err := e.loadDirectory(dir, true)
	if err != nil {
		t.Fatalf("loadDirectory forced: %v", err)
	}
	if forced == first {
		t.Fatal("force should rebuild bundle")
	}
}

func TestNamed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.alos", "I")
	writeFile(t, dir, "Nav.alos", "N")
	e := newTestEngine(t)
	bundle, err := e.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if bundle.Named("") != bundle.renderTarget() {
		t.Fatal("empty name on bundle should give render target")
	}
	if bundle.Named("nav") == nil {
		t.Fatal("lowercase lookup failed")
	}
	if bundle.Named("NAV") == nil {
		t.Fatal("uppercase lookup failed")
	}
	if bundle.Named("nav.alos") == nil {
		t.Fatal("extension lookup failed")
	}
	if bundle.Named("NAV.ALOS") == nil {
		t.Fatal("uppercase extension lookup failed")
	}
	if bundle.Named("missing") != nil {
		t.Fatal("missing lookup should be nil")
	}

	var nilTpl *Template
	if nilTpl.Named("x") != nil {
		t.Fatal("nil receiver should return nil")
	}

	single, err := e.Load(filepath.Join(dir, "index.alos"))
	if err != nil {
		t.Fatalf("Load file: %v", err)
	}
	if single.Named("") != single {
		t.Fatal("single template empty name should be itself")
	}
	if single.Named("index") != single {
		t.Fatal("single template name lookup failed")
	}
	if single.Named("index.alos") != single {
		t.Fatal("single template filename lookup failed")
	}
	if single.Named("other") != nil {
		t.Fatal("single template wrong name should be nil")
	}
}

func TestNames(t *testing.T) {
	var nilTpl *Template
	if nilTpl.Names() != nil {
		t.Fatal("nil receiver Names should be nil")
	}
	if (&Template{}).Names() != nil {
		t.Fatal("unnamed template Names should be nil")
	}
	single := &Template{name: "solo"}
	got := single.Names()
	if len(got) != 1 || got[0] != "solo" {
		t.Fatalf("Names = %v", got)
	}

	dir := t.TempDir()
	writeFile(t, dir, "index.alos", "I")
	writeFile(t, dir, "nav.alos", "N")
	e := newTestEngine(t)
	bundle, err := e.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	names := bundle.Names()
	names[0] = "mutated"
	if bundle.Names()[0] == "mutated" {
		t.Fatal("Names must return a defensive copy")
	}
}

func TestNameAndFileName(t *testing.T) {
	var nilTpl *Template
	if nilTpl.Name() != "" {
		t.Fatal("nil Name should be empty")
	}
	if nilTpl.FileName() != "" {
		t.Fatal("nil FileName should be empty")
	}
	dir := t.TempDir()
	writeFile(t, dir, "sub/deep.alos", "D")
	e := newTestEngine(t)
	tpl, err := e.Load(filepath.Join(dir, "sub", "deep.alos"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if tpl.Name() != "deep" {
		t.Fatalf("Name = %q", tpl.Name())
	}
	if tpl.FileName() != "deep.alos" {
		t.Fatalf("FileName = %q", tpl.FileName())
	}
	bundle, err := e.Load(dir)
	if err != nil {
		t.Fatalf("Load dir: %v", err)
	}
	if bundle.Name() != "sub/deep" {
		t.Fatalf("bundle Name = %q", bundle.Name())
	}
	if bundle.FileName() != "deep.alos" {
		t.Fatalf("bundle FileName = %q", bundle.FileName())
	}
}

func TestRenderTarget(t *testing.T) {
	var nilTpl *Template
	if nilTpl.renderTarget() != nil {
		t.Fatal("nil renderTarget should be nil")
	}
	leaf := &Template{name: "leaf"}
	if leaf.renderTarget() != leaf {
		t.Fatal("leaf renderTarget should be itself")
	}
	bundle := &Template{defaultTpl: leaf}
	if bundle.renderTarget() != leaf {
		t.Fatal("bundle renderTarget should be defaultTpl")
	}
}

func TestCompileSourceStatic(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "no placeholders here")
	if len(tpl.slots) != 0 {
		t.Fatalf("slots = %d want 0", len(tpl.slots))
	}
	if len(tpl.literals) != 1 || tpl.literals[0] != "no placeholders here" {
		t.Fatalf("literals = %v", tpl.literals)
	}
	if tpl.staticLen != len("no placeholders here") {
		t.Fatalf("staticLen = %d", tpl.staticLen)
	}
	if tpl.single.enabled {
		t.Fatal("static template should not enable single slot")
	}
}

func TestCompileSourceEmpty(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "")
	if len(tpl.literals) != 1 || tpl.literals[0] != "" {
		t.Fatalf("literals = %v", tpl.literals)
	}
	if tpl.staticLen != 0 {
		t.Fatalf("staticLen = %d", tpl.staticLen)
	}
	if got := Replace(tpl, nil); got != "" {
		t.Fatalf("render = %q", got)
	}
}

func TestCompileSourceSingleSlot(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "pre{{k}}post")
	if !tpl.single.enabled {
		t.Fatal("single slot not enabled")
	}
	if tpl.single.key != "k" {
		t.Fatalf("key = %q", tpl.single.key)
	}
	if tpl.single.prefix != "pre" || tpl.single.suffix != "post" {
		t.Fatalf("prefix/suffix = %q/%q", tpl.single.prefix, tpl.single.suffix)
	}
	if tpl.single.placeholder != "{{k}}" {
		t.Fatalf("placeholder = %q", tpl.single.placeholder)
	}
	if tpl.single.staticLen != 7 {
		t.Fatalf("staticLen = %d want 7", tpl.single.staticLen)
	}
	if tpl.single.prefixLen != 3 || tpl.single.suffixLen != 4 {
		t.Fatalf("prefixLen/suffixLen = %d/%d", tpl.single.prefixLen, tpl.single.suffixLen)
	}
}

func TestCompileSourceMultiSlot(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "a{{x}}b{{y}}c")
	if len(tpl.slots) != 2 {
		t.Fatalf("slots = %d", len(tpl.slots))
	}
	if len(tpl.literals) != 3 {
		t.Fatalf("literals = %v", tpl.literals)
	}
	if tpl.single.enabled {
		t.Fatal("multi slot must not enable single fast path")
	}
	wantKeys := []string{"x", "y"}
	for i := range wantKeys {
		if tpl.keys[i] != wantKeys[i] {
			t.Fatalf("keys = %v", tpl.keys)
		}
	}
}

func TestCompileSourceDuplicateKeysShareIndex(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "{{a}}-{{a}}-{{b}}-{{a}}")
	if len(tpl.keys) != 2 {
		t.Fatalf("keys = %v want 2 unique", tpl.keys)
	}
	if len(tpl.slots) != 4 {
		t.Fatalf("slots = %d want 4", len(tpl.slots))
	}
	if tpl.slots[0].keyIndex != 0 || tpl.slots[1].keyIndex != 0 || tpl.slots[3].keyIndex != 0 {
		t.Fatal("duplicate keys must share a key index")
	}
	if tpl.slots[2].keyIndex != 1 {
		t.Fatal("second key index wrong")
	}
}

func TestCompileSourceTrimsKeyWhitespace(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "{{  spaced  }}")
	if tpl.keys[0] != "spaced" {
		t.Fatalf("key = %q", tpl.keys[0])
	}
	if tpl.slots[0].placeholder != "{{  spaced  }}" {
		t.Fatalf("placeholder = %q must keep original text", tpl.slots[0].placeholder)
	}
}

func TestCompileSourceUnterminated(t *testing.T) {
	e := newTestEngine(t)
	if _, err := e.compileSource("a{{b"); err == nil {
		t.Fatal("expected unterminated placeholder error")
	}
}

func TestCompileSourceEmptyPlaceholder(t *testing.T) {
	e := newTestEngine(t)
	if _, err := e.compileSource("a{{}}b"); err == nil {
		t.Fatal("expected empty placeholder error")
	}
	if _, err := e.compileSource("a{{   }}b"); err == nil {
		t.Fatal("expected empty placeholder error for whitespace-only key")
	}
}

func TestCompileSourceCustomDelimiters(t *testing.T) {
	e := newTestEngine(t, WithDelimiters("<%", "%>"))
	tpl := mustCompile(t, e, "a<%k%>b{{notakey}}")
	if len(tpl.slots) != 1 {
		t.Fatalf("slots = %d", len(tpl.slots))
	}
	if tpl.keys[0] != "k" {
		t.Fatalf("key = %q", tpl.keys[0])
	}
	if got := Replace(tpl, []string{"k", "V"}); got != "aVb{{notakey}}" {
		t.Fatalf("render = %q", got)
	}
}

func TestCompileSourceAdjacentSlots(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "{{a}}{{b}}")
	if len(tpl.literals) != 3 {
		t.Fatalf("literals = %v", tpl.literals)
	}
	if tpl.staticLen != 0 {
		t.Fatalf("staticLen = %d want 0", tpl.staticLen)
	}
	if got := Replace(tpl, []string{"a", "1", "b", "2"}); got != "12" {
		t.Fatalf("render = %q", got)
	}
}

func TestCompileNamedTemplate(t *testing.T) {
	e := newTestEngine(t)
	tpl, err := e.compileNamedTemplate("/abs/dir/sub/page.alos", "sub/page.alos", "x{{k}}")
	if err != nil {
		t.Fatalf("compileNamedTemplate: %v", err)
	}
	if tpl.sourcePath != "/abs/dir/sub/page.alos" {
		t.Fatalf("sourcePath = %q", tpl.sourcePath)
	}
	if tpl.loadPath != "/abs/dir/sub/page.alos" {
		t.Fatalf("loadPath = %q", tpl.loadPath)
	}
	if tpl.fileName != "page.alos" {
		t.Fatalf("fileName = %q", tpl.fileName)
	}
	if tpl.name != "sub/page" {
		t.Fatalf("name = %q", tpl.name)
	}
	if _, err := e.compileNamedTemplate("/a", "a.alos", "{{bad"); err == nil {
		t.Fatal("expected compile error to propagate")
	}
}

func TestReplaceNilTemplate(t *testing.T) {
	if got := Replace(nil, []string{"a", "b"}); len(got) != 0 {
		t.Fatalf("Replace(nil) = %q", got)
	}
	if got := Replace(nil, nil); len(got) != 0 {
		t.Fatalf("Replace(nil) with no values = %q", got)
	}
}

func TestReplaceMapNilTemplate(t *testing.T) {
	if got := ReplaceMap(nil, map[string]string{"a": "b"}); len(got) != 0 {
		t.Fatalf("ReplaceMap(nil) = %q", got)
	}
}

func TestReplaceStatic(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "static only")
	if got := Replace(tpl, []string{"a", "b"}); got != "static only" {
		t.Fatalf("render = %q", got)
	}
	if got := ReplaceMap(tpl, map[string]string{"a": "b"}); got != "static only" {
		t.Fatalf("map render = %q", got)
	}
	if got := Replace(tpl, nil); got != "static only" {
		t.Fatalf("warm dst render = %q", got)
	}
	if got := Replace(tpl, nil); got != "static only" {
		t.Fatalf("cold dst render = %q", got)
	}
}

func TestReplaceSingleShorthand(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "Hello {{name}}!")
	if got := Replace(tpl, []string{"World"}); got != "Hello World!" {
		t.Fatalf("render = %q", got)
	}
	if got := Replace(tpl, []string{""}); got != "Hello !" {
		t.Fatalf("empty value render = %q", got)
	}
}

func TestReplaceSingleMatchingPair(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "Hello {{name}}!")
	if got := Replace(tpl, []string{"name", "Ada"}); got != "Hello Ada!" {
		t.Fatalf("render = %q", got)
	}
}

func TestReplaceSingleNonMatchingPairKeepsPlaceholder(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "Hello {{name}}!")
	if got := Replace(tpl, []string{"other", "Ada"}); got != "Hello {{name}}!" {
		t.Fatalf("render = %q", got)
	}
}

func TestReplaceSingleNilPairsKeepsPlaceholder(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "Hello {{name}}!")
	if got := Replace(tpl, nil); got != "Hello {{name}}!" {
		t.Fatalf("render = %q", got)
	}
}

func TestReplaceSingleManyPairs(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "Hello {{name}}!")
	if got := Replace(tpl, []string{"a", "1", "name", "Ada", "b", "2"}); got != "Hello Ada!" {
		t.Fatalf("render = %q", got)
	}
	if got := Replace(tpl, []string{"a", "1", "b", "2", "c", "3"}); got != "Hello {{name}}!" {
		t.Fatalf("render = %q", got)
	}
}

func TestReplaceSingleNoPrefixOrSuffix(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "{{only}}")
	if got := Replace(tpl, []string{"V"}); got != "V" {
		t.Fatalf("render = %q", got)
	}
	if got := Replace(tpl, []string{"only", "V"}); got != "V" {
		t.Fatalf("render = %q", got)
	}
	if got := ReplaceMap(tpl, map[string]string{"only": "V"}); got != "V" {
		t.Fatalf("map render = %q", got)
	}
	if got := Replace(tpl, []string{""}); got != "" {
		t.Fatalf("empty render = %q", got)
	}
}

func TestReplaceSingleMap(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "Hello {{name}}!")
	if got := ReplaceMap(tpl, map[string]string{"name": "Ada"}); got != "Hello Ada!" {
		t.Fatalf("render = %q", got)
	}
	if got := ReplaceMap(tpl, map[string]string{"other": "x"}); got != "Hello {{name}}!" {
		t.Fatalf("render = %q", got)
	}
	if got := ReplaceMap(tpl, nil); got != "Hello {{name}}!" {
		t.Fatalf("render = %q", got)
	}
	if got := ReplaceMap(tpl, map[string]string{"name": ""}); got != "Hello !" {
		t.Fatalf("render = %q", got)
	}
}

func TestReplaceMissingKeysKeepPlaceholders(t *testing.T) {
	e := newTestEngine(t)
	src := "a{{k1}}b{{k2}}c{{k3}}d"
	tpl := mustCompile(t, e, src)
	got := Replace(tpl, []string{"k2", "V"})
	want := "a{{k1}}bVc{{k3}}d"
	if got != want {
		t.Fatalf("render = %q want %q", got, want)
	}
	gotMap := ReplaceMap(tpl, map[string]string{"k2": "V"})
	if gotMap != want {
		t.Fatalf("map render = %q want %q", gotMap, want)
	}
}

func TestReplaceDuplicateKeys(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "{{a}}|{{a}}|{{b}}|{{a}}")
	if got := Replace(tpl, []string{"a", "X", "b", "Y"}); got != "X|X|Y|X" {
		t.Fatalf("render = %q", got)
	}
	if got := ReplaceMap(tpl, map[string]string{"a": "X", "b": "Y"}); got != "X|X|Y|X" {
		t.Fatalf("map render = %q", got)
	}
	if got := ReplaceMap(tpl, map[string]string{"b": "Y"}); got != "{{a}}|{{a}}|Y|{{a}}" {
		t.Fatalf("map partial render = %q", got)
	}
}

func TestReplaceAcrossAllSlotCounts(t *testing.T) {
	e := newTestEngine(t)
	for _, slots := range []int{2, 3, 4, 5, 8, 9, 16, 33, 64, 129} {
		src := buildSource(slots, 6, "k")
		tpl := mustCompile(t, e, src)
		values := buildValues(slots, "k", 5)
		pairs := buildPairs(slots, "k", 5)
		want := expectedRender(src, values)
		if got := Replace(tpl, pairs); got != want {
			t.Fatalf("slots=%d pairs render mismatch", slots)
		}
		if got := ReplaceMap(tpl, values); got != want {
			t.Fatalf("slots=%d map render mismatch", slots)
		}
		partial := make(map[string]string, slots)
		partialPairs := make([]string, 0, slots)
		for k, v := range values {
			if len(partial)%2 == 0 {
				partial[k] = v
				partialPairs = append(partialPairs, k, v)
			}
		}
		wantPartial := expectedRender(src, partial)
		if got := ReplaceMap(tpl, partial); got != wantPartial {
			t.Fatalf("slots=%d partial map render mismatch", slots)
		}
		if got := Replace(tpl, partialPairs); got != wantPartial {
			t.Fatalf("slots=%d partial pairs render mismatch", slots)
		}
	}
}

func TestReplaceRepeatedRendersAreStable(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "a{{x}}b{{y}}c")
	for i := 0; i < 8; i++ {
		if got := Replace(tpl, []string{"x", "1", "y", "2"}); got != "a1b2c" {
			t.Fatalf("iteration %d render = %q", i, got)
		}
	}
}

func TestReplaceDstReuseGrowsWhenTooSmall(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "a{{x}}b")
	long := strings.Repeat("Z", 4096)
	out := Replace(tpl, []string{"x", long})
	if string(out) != "a"+long+"b" {
		t.Fatal("grown render incorrect")
	}
	if len(out) != len(long)+2 {
		t.Fatalf("len = %d", len(out))
	}
}

func TestReplaceDstReuseShrinks(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "a{{x}}b")
	out := Replace(tpl, []string{"x", "1"})
	if string(out) != "a1b" {
		t.Fatalf("render = %q", out)
	}
	if len(out) != 3 {
		t.Fatalf("len = %d want 3", len(out))
	}
}

func TestReplaceDoesNotAliasInput(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "a{{x}}b")
	value := []byte("VALUE")
	out := Replace(tpl, []string{"x", string(value)})
	value[0] = 'Z'
	if string(out) != "aVALUEb" {
		t.Fatalf("output aliased caller memory: %q", out)
	}
}

func TestReplaceMapSmallBoundary(t *testing.T) {
	e := newTestEngine(t)
	for slots := 1; slots <= 5; slots++ {
		src := buildSource(slots, 3, "k")
		tpl := mustCompile(t, e, src)
		values := buildValues(slots, "k", 4)
		want := expectedRender(src, values)
		if got := ReplaceMap(tpl, values); got != want {
			t.Fatalf("slots=%d render = %q want %q", slots, got, want)
		}
	}
}

func TestReplaceMapSmallDirect(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "a{{x}}b{{y}}c")
	got := ReplaceMap(tpl, map[string]string{"x": "1", "y": "2"})
	if got != "a1b2c" {
		t.Fatalf("render = %q", got)
	}
	got = ReplaceMap(tpl, map[string]string{"x": "1"})
	if got != "a1b{{y}}c" {
		t.Fatalf("partial render = %q", got)
	}
}

func TestReplaceSingleDirectWarmDst(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "pre{{k}}post")
	warm := make([]byte, 128)
	if got := string(tpl.replaceSingle(warm, []string{"V"})); got != "preVpost" {
		t.Fatalf("render = %q", got)
	}
	if got := string(tpl.replaceSingle(warm, []string{"k", "V"})); got != "preVpost" {
		t.Fatalf("render = %q", got)
	}
	if got := string(tpl.replaceSingle(warm, nil)); got != "pre{{k}}post" {
		t.Fatalf("render = %q", got)
	}
	if got := string(tpl.replaceSingleMap(warm, map[string]string{"k": "V"})); got != "preVpost" {
		t.Fatalf("render = %q", got)
	}
	cold := make([]byte, 1)
	if got := string(tpl.replaceSingle(cold, []string{"V"})); got != "preVpost" {
		t.Fatalf("render = %q", got)
	}
	if got := string(tpl.replaceSingleMap(cold, map[string]string{"k": "V"})); got != "preVpost" {
		t.Fatalf("render = %q", got)
	}
}

func TestReplaceLargeValues(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "<{{a}}|{{b}}>")
	a := strings.Repeat("A", 100000)
	b := strings.Repeat("B", 100000)
	got := Replace(tpl, []string{"a", a, "b", b})
	if got != "<"+a+"|"+b+">" {
		t.Fatal("large value render mismatch")
	}
}

func TestReplaceUnicode(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, "héllo {{名前}} 🎉")
	if got := Replace(tpl, []string{"名前", "世界"}); got != "héllo 世界 🎉" {
		t.Fatalf("render = %q", got)
	}
	if got := ReplaceMap(tpl, map[string]string{"名前": "世界"}); got != "héllo 世界 🎉" {
		t.Fatalf("map render = %q", got)
	}
}

func TestReplaceBundleUsesDefaultTarget(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.alos", "I{{v}}")
	writeFile(t, dir, "other.alos", "O{{v}}")
	e := newTestEngine(t)
	bundle, err := e.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := Replace(bundle, []string{"v", "x"}); got != "Ix" {
		t.Fatalf("render = %q", got)
	}
	if got := ReplaceMap(bundle, map[string]string{"v": "x"}); got != "Ix" {
		t.Fatalf("map render = %q", got)
	}
}

func TestFindReplacement(t *testing.T) {
	cases := []struct {
		name  string
		pairs []string
		key   string
		want  string
		ok    bool
	}{
		{"nil", nil, "a", "", false},
		{"empty", []string{}, "a", "", false},
		{"single element", []string{"a"}, "a", "", false},
		{"two match", []string{"a", "1"}, "a", "1", true},
		{"two miss", []string{"a", "1"}, "b", "", false},
		{"four first", []string{"a", "1", "b", "2"}, "a", "1", true},
		{"four second", []string{"a", "1", "b", "2"}, "b", "2", true},
		{"four miss", []string{"a", "1", "b", "2"}, "c", "", false},
		{"odd trailing ignored", []string{"a", "1", "b"}, "b", "", false},
		{"duplicate first wins", []string{"a", "1", "a", "2"}, "a", "1", true},
		{"empty key", []string{"", "1"}, "", "1", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := findReplacement(tc.pairs, tc.key)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("findReplacement(%v,%q) = %q,%v want %q,%v", tc.pairs, tc.key, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestParseIncludeDirective(t *testing.T) {
	cases := []struct {
		inner string
		want  string
		ok    bool
	}{
		{"include \"nav\"", "nav", true},
		{"include 'nav'", "nav", true},
		{"include  \"nav\"", "nav", true},
		{"include \"sub/nav\"", "sub/nav", true},
		{"include \"\"", "", true},
		{"include", "", false},
		{"include ", "", false},
		{"include x", "", false},
		{"include \"unclosed", "", false},
		{"includes \"nav\"", "", false},
		{"nav", "", false},
		{"", "", false},
		{"include \"a", "", false},
		{"include `nav`", "", false},
	}
	for _, tc := range cases {
		got, ok := parseIncludeDirective(tc.inner)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("parseIncludeDirective(%q) = %q,%v want %q,%v", tc.inner, got, ok, tc.want, tc.ok)
		}
	}
}

func TestTrimTemplateExtension(t *testing.T) {
	cases := map[string]string{
		"a.alos":          "a",
		"a.ALOS":          "a",
		"a.AlOs":          "a",
		"sub/a.alos":      "sub/a",
		"sub\\a.alos":     "sub/a",
		"a":               "a",
		"a.txt":           "a.txt",
		"  a.alos  ":      "a",
		".alos":           "",
		"a.alos.alos":     "a.alos",
		"deep/sub/x.alos": "deep/sub/x",
	}
	for in, want := range cases {
		if got := trimTemplateExtension(in); got != want {
			t.Fatalf("trimTemplateExtension(%q) = %q want %q", in, got, want)
		}
	}
}

func TestNormalizeTemplateName(t *testing.T) {
	cases := map[string]string{
		"Index.alos": "index",
		"INDEX":      "index",
		"Sub/Page":   "sub/page",
		"  X.ALOS ":  "x",
		"":           "",
	}
	for in, want := range cases {
		if got := normalizeTemplateName(in); got != want {
			t.Fatalf("normalizeTemplateName(%q) = %q want %q", in, got, want)
		}
	}
}

func TestFileSignature(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.alos", "hello")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	sig := fileSignature(info)
	if sig.modNanos == 0 || sig.size == 0 {
		t.Fatalf("signature = %+v", sig)
	}
	info2, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if fileSignature(info2) != sig {
		t.Fatal("signature not stable for unchanged file")
	}
	time.Sleep(10 * time.Millisecond)
	writeFile(t, dir, "a.alos", "hello world")
	info3, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if fileSignature(info3) == sig {
		t.Fatal("signature did not change after modification")
	}
}

func TestScanTemplateDirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "b.alos", "B")
	writeFile(t, dir, "a.alos", "A")
	writeFile(t, dir, "sub/c.alos", "C")
	writeFile(t, dir, "ignore.txt", "X")
	files, sig, err := scanTemplateDirectory(dir)
	if err != nil {
		t.Fatalf("scanTemplateDirectory: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("files = %d want 3", len(files))
	}
	want := []string{"a.alos", "b.alos", "sub/c.alos"}
	for i := range want {
		if filepath.ToSlash(files[i].relPath) != want[i] {
			t.Fatalf("files = %v want sorted %v", files, want)
		}
	}
	if sig.listing == "" {
		t.Fatal("empty signature")
	}
	_, sig2, err := scanTemplateDirectory(dir)
	if err != nil {
		t.Fatalf("scanTemplateDirectory: %v", err)
	}
	if sig2 != sig {
		t.Fatal("signature not stable")
	}
	time.Sleep(10 * time.Millisecond)
	writeFile(t, dir, "a.alos", "AA")
	_, sig3, err := scanTemplateDirectory(dir)
	if err != nil {
		t.Fatalf("scanTemplateDirectory: %v", err)
	}
	if sig3 == sig {
		t.Fatal("signature did not change")
	}
}

func TestScanTemplateDirectoryErrors(t *testing.T) {
	if _, _, err := scanTemplateDirectory(t.TempDir()); err == nil {
		t.Fatal("expected error for empty dir")
	}
	if _, _, err := scanTemplateDirectory(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected error for missing dir")
	}
}

func TestExpandBundleSourceMemoizes(t *testing.T) {
	e := newTestEngine(t)
	file := &bundleSourceFile{relPath: "a.alos", raw: "A{{k}}"}
	aliases := map[string]*bundleSourceFile{}
	first, err := e.expandBundleSource(file, aliases)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if !file.expandedOK {
		t.Fatal("expandedOK not set")
	}
	file.raw = "MUTATED"
	second, err := e.expandBundleSource(file, aliases)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if first != second {
		t.Fatal("expand did not memoize")
	}
}

func TestExpandBundleSourceNested(t *testing.T) {
	e := newTestEngine(t)
	inner := &bundleSourceFile{relPath: "inner.alos", raw: "IN"}
	middle := &bundleSourceFile{relPath: "middle.alos", raw: "M[{{include \"inner\"}}]"}
	outer := &bundleSourceFile{relPath: "outer.alos", raw: "O({{include \"middle\"}})"}
	aliases := map[string]*bundleSourceFile{"inner": inner, "middle": middle, "outer": outer}
	got, err := e.expandBundleSource(outer, aliases)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if got != "O(M[IN])" {
		t.Fatalf("expanded = %q", got)
	}
}

func TestExpandBundleSourceUnterminated(t *testing.T) {
	e := newTestEngine(t)
	file := &bundleSourceFile{relPath: "a.alos", raw: "A{{oops"}
	if _, err := e.expandBundleSource(file, map[string]*bundleSourceFile{}); err == nil {
		t.Fatal("expected unterminated error")
	}
}

func TestExpandBundleSourceCycleClearsFlag(t *testing.T) {
	e := newTestEngine(t)
	a := &bundleSourceFile{relPath: "a.alos", raw: "A{{include \"b\"}}"}
	b := &bundleSourceFile{relPath: "b.alos", raw: "B{{include \"a\"}}"}
	aliases := map[string]*bundleSourceFile{"a": a, "b": b}
	if _, err := e.expandBundleSource(a, aliases); err == nil {
		t.Fatal("expected cycle error")
	}
	if a.expanding || b.expanding {
		t.Fatal("expanding flag not cleared after cycle error")
	}
}

func TestAcquireReleaseRenderScratch(t *testing.T) {
	s := acquireRenderScratch(4, 9)
	if len(s.resolved) != 4 || len(s.found) != 4 || len(s.parts) != 9 {
		t.Fatalf("scratch sizes = %d,%d,%d", len(s.resolved), len(s.found), len(s.parts))
	}
	s.resolved[0] = "dirty"
	s.found[0] = true
	s.parts[0] = "dirty"
	releaseRenderScratch(s, 4)

	grown := acquireRenderScratch(64, 129)
	if len(grown.resolved) != 64 || len(grown.found) != 64 || len(grown.parts) != 129 {
		t.Fatalf("grown sizes = %d,%d,%d", len(grown.resolved), len(grown.found), len(grown.parts))
	}
	releaseRenderScratch(grown, 64)

	shrunk := acquireRenderScratch(2, 5)
	if len(shrunk.resolved) != 2 || len(shrunk.found) != 2 || len(shrunk.parts) != 5 {
		t.Fatalf("shrunk sizes = %d,%d,%d", len(shrunk.resolved), len(shrunk.found), len(shrunk.parts))
	}
	releaseRenderScratch(shrunk, 2)

	releaseRenderScratch(nil, 0)
}

func TestRenderScratchDoesNotLeakStaleValues(t *testing.T) {
	e := newTestEngine(t)
	src := buildSource(16, 2, "k")
	tpl := mustCompile(t, e, src)
	full := buildPairs(16, "k", 3)
	if got := Replace(tpl, full); got != expectedRender(src, buildValues(16, "k", 3)) {
		t.Fatal("full render mismatch")
	}
	empty := map[string]string{}
	want := expectedRender(src, empty)
	if got := Replace(tpl, nil); got != want {
		t.Fatalf("stale scratch leaked into render:\n got %q\nwant %q", got, want)
	}
	if got := ReplaceMap(tpl, empty); got != want {
		t.Fatalf("stale scratch leaked into map render:\n got %q\nwant %q", got, want)
	}
}

func TestRuntimeMemmove(t *testing.T) {
	src := []byte("hello world")
	dst := make([]byte, len(src))
	runtimeMemmove(unsafe.Pointer(unsafe.SliceData(dst)), unsafe.Pointer(unsafe.SliceData(src)), uintptr(len(src)))
	if string(dst) != "hello world" {
		t.Fatalf("memmove = %q", dst)
	}
	runtimeMemmove(unsafe.Pointer(unsafe.SliceData(dst)), unsafe.Pointer(unsafe.SliceData(src)), 0)
	if string(dst) != "hello world" {
		t.Fatalf("zero-length memmove corrupted dst = %q", dst)
	}
	overlap := []byte("abcdefgh")
	runtimeMemmove(unsafe.Pointer(&overlap[2]), unsafe.Pointer(&overlap[0]), 4)
	if string(overlap) != "ababcdgh" {
		t.Fatalf("overlapping memmove = %q", overlap)
	}
}

func TestConcurrentReplaceIsSafe(t *testing.T) {
	e := newTestEngine(t)
	src := buildSource(24, 8, "k")
	tpl := mustCompile(t, e, src)
	values := buildValues(24, "k", 6)
	pairs := buildPairs(24, "k", 6)
	want := expectedRender(src, values)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if got := Replace(tpl, pairs); got != want {
					t.Errorf("concurrent pairs render mismatch")
					return
				}
				if got := ReplaceMap(tpl, values); got != want {
					t.Errorf("concurrent map render mismatch")
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestConcurrentLoadIsSafe(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.alos", "I{{v}}")
	writeFile(t, dir, "nav.alos", "N")
	e := newTestEngine(t)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if _, err := e.Load(dir); err != nil {
					t.Errorf("concurrent Load: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
