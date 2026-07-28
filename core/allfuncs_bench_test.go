package core

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
	"unsafe"
)

var (
	sinkBytes    []byte
	sinkString   string
	sinkStrings  []string
	sinkInt      int
	sinkU64      uint64
	sinkBool     bool
	sinkTpl      *Template
	sinkEngine   *Engine
	sinkErr      error
	sinkDur      time.Duration
	sinkKeyTable keyTable
	sinkScratch  *renderScratch
	sinkParts    []string
	sinkSig      cacheSignature
	sinkFiles    []bundleSourceFile
)

const (
	bmSlots      = 16
	bmLiteralLen = 512
)

func bmSource() string { return buildSource(bmSlots, bmLiteralLen, "k") }

func bmEngineTemplate(b *testing.B) (*Engine, *Template, []string, map[string]string) {
	b.Helper()
	e := newTestEngine(b)
	tpl := mustCompile(b, e, bmSource())
	pairs := buildPairs(bmSlots, "k", 24)
	values := buildValues(bmSlots, "k", 24)
	return e, tpl, pairs, values
}

func bmDirTemplate(b *testing.B) (*Engine, string) {
	b.Helper()
	e := newTestEngine(b)
	dir := b.TempDir()
	path := filepath.Join(dir, "page.alos")
	if err := os.WriteFile(path, []byte(bmSource()), 0o600); err != nil {
		b.Fatal(err)
	}
	return e, path
}

func bmBundleDir(b *testing.B) (*Engine, string) {
	b.Helper()
	e := newTestEngine(b)
	dir := b.TempDir()
	for i := 0; i < 8; i++ {
		p := filepath.Join(dir, "p"+strconv.Itoa(i)+".alos")
		if err := os.WriteFile(p, []byte(bmSource()), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "index.alos"), []byte(bmSource()), 0o600); err != nil {
		b.Fatal(err)
	}
	return e, dir
}

// ---------- construction / options ----------

func BenchmarkFn_WithDelimiters(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		opt := WithDelimiters("<%", "%>")
		e := &Engine{}
		opt(e)
		sinkEngine = e
	}
}

func BenchmarkFn_WithAutoRefresh(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		opt := WithAutoRefresh(30 * time.Second)
		e := &Engine{}
		opt(e)
		sinkEngine = e
	}
}

func BenchmarkFn_WithModifiedOnly(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		opt := WithModifiedOnly(true)
		e := &Engine{}
		opt(e)
		sinkEngine = e
	}
}

func BenchmarkFn_NewEngine(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		e := NewEngine()
		sinkEngine = e
		e.Stop()
	}
}

func BenchmarkFn_Stop(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		e := NewEngine()
		b.StartTimer()
		e.Stop()
	}
}

func BenchmarkFn_Delimiters(b *testing.B) {
	e := newTestEngine(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkString, _ = e.Delimiters()
	}
}

func BenchmarkFn_SetDelimiters(b *testing.B) {
	e := newTestEngine(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.SetDelimiters("{{", "}}")
	}
}

func BenchmarkFn_SetAutoRefresh(b *testing.B) {
	e := newTestEngine(b)
	b.Cleanup(e.Stop)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.SetAutoRefresh(0)
	}
}

func BenchmarkFn_AutoRefresh(b *testing.B) {
	e := newTestEngine(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkDur = e.AutoRefresh()
	}
}

func BenchmarkFn_autoRefreshLoop_StartStop(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		e := NewEngine(WithAutoRefresh(time.Hour))
		e.Stop()
	}
}

// ---------- load path: cached vs uncached ----------

func BenchmarkFn_Load_Cached(b *testing.B) {
	e, path := bmDirTemplate(b)
	if _, err := e.Load(path); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkTpl, sinkErr = e.Load(path)
	}
}

func BenchmarkFn_Load_Uncached(b *testing.B) {
	e, path := bmDirTemplate(b)
	abs, _ := filepath.Abs(path)
	if _, err := e.Load(path); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkTpl, sinkErr = e.loadResolved(abs, true)
	}
}

func BenchmarkFn_loadResolved_Cached(b *testing.B) {
	e, path := bmDirTemplate(b)
	abs, _ := filepath.Abs(path)
	if _, err := e.Load(path); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkTpl, sinkErr = e.loadResolved(abs, false)
	}
}

func BenchmarkFn_loadFile_Cached(b *testing.B) {
	e, path := bmDirTemplate(b)
	abs, _ := filepath.Abs(path)
	info, err := os.Stat(abs)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := e.Load(path); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkTpl, sinkErr = e.loadFile(abs, info, false)
	}
}

func BenchmarkFn_loadFile_Uncached(b *testing.B) {
	e, path := bmDirTemplate(b)
	abs, _ := filepath.Abs(path)
	info, err := os.Stat(abs)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkTpl, sinkErr = e.loadFile(abs, info, true)
	}
}

func BenchmarkFn_loadDirectory_Cached(b *testing.B) {
	e, dir := bmBundleDir(b)
	abs, _ := filepath.Abs(dir)
	if _, err := e.Load(dir); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkTpl, sinkErr = e.loadDirectory(abs, false)
	}
}

func BenchmarkFn_loadDirectory_Uncached(b *testing.B) {
	e, dir := bmBundleDir(b)
	abs, _ := filepath.Abs(dir)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkTpl, sinkErr = e.loadDirectory(abs, true)
	}
}

func BenchmarkFn_EngineReload(b *testing.B) {
	e, path := bmDirTemplate(b)
	if _, err := e.Load(path); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkErr = e.Reload()
	}
}

func BenchmarkFn_TemplateReload(b *testing.B) {
	e, path := bmDirTemplate(b)
	tpl, err := e.Load(path)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkErr = tpl.Reload()
	}
}

func BenchmarkFn_applyTemplateReload(b *testing.B) {
	e, _, _, _ := bmEngineTemplate(b)
	src := mustCompile(b, e, bmSource())
	dst := mustCompile(b, e, bmSource())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		applyTemplateReload(dst, src)
	}
}

// ---------- template accessors ----------

func BenchmarkFn_Named(b *testing.B) {
	e, dir := bmBundleDir(b)
	bundle, err := e.Load(dir)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkTpl = bundle.Named("p3")
	}
}

func BenchmarkFn_Names(b *testing.B) {
	e, dir := bmBundleDir(b)
	bundle, err := e.Load(dir)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkStrings = bundle.Names()
	}
}

func BenchmarkFn_Name(b *testing.B) {
	_, tpl, _, _ := bmEngineTemplate(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkString = tpl.Name()
	}
}

func BenchmarkFn_FileName(b *testing.B) {
	_, tpl, _, _ := bmEngineTemplate(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkString = tpl.FileName()
	}
}

func BenchmarkFn_renderTarget(b *testing.B) {
	_, tpl, _, _ := bmEngineTemplate(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkTpl = tpl.renderTarget()
	}
}

// ---------- render hot path: fresh buffer (uncached) vs reused buffer ----------

func BenchmarkFn_Replace_FreshBuffer(b *testing.B) {
	_, tpl, pairs, _ := bmEngineTemplate(b)
	warm := Replace(tpl, nil, pairs)
	b.ReportAllocs()
	b.SetBytes(int64(len(warm)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkBytes = Replace(tpl, nil, pairs)
	}
}

func BenchmarkFn_Replace_ReusedBuffer(b *testing.B) {
	_, tpl, pairs, _ := bmEngineTemplate(b)
	dst := Replace(tpl, nil, pairs)
	b.ReportAllocs()
	b.SetBytes(int64(len(dst)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dst = Replace(tpl, dst, pairs)
	}
	sinkBytes = dst
}

func BenchmarkFn_ReplaceMap_FreshBuffer(b *testing.B) {
	_, tpl, _, values := bmEngineTemplate(b)
	warm := ReplaceMap(tpl, nil, values)
	b.ReportAllocs()
	b.SetBytes(int64(len(warm)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkBytes = ReplaceMap(tpl, nil, values)
	}
}

func BenchmarkFn_ReplaceMap_ReusedBuffer(b *testing.B) {
	_, tpl, _, values := bmEngineTemplate(b)
	dst := ReplaceMap(tpl, nil, values)
	b.ReportAllocs()
	b.SetBytes(int64(len(dst)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dst = ReplaceMap(tpl, dst, values)
	}
	sinkBytes = dst
}

func BenchmarkFn_renderStatic_FreshBuffer(b *testing.B) {
	e := newTestEngine(b)
	tpl := mustCompile(b, e, buildSource(0, bmLiteralLen*bmSlots, "k"))
	warm := tpl.renderStatic(nil)
	b.ReportAllocs()
	b.SetBytes(int64(len(warm)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkBytes = tpl.renderStatic(nil)
	}
}

func BenchmarkFn_renderStatic_ReusedBuffer(b *testing.B) {
	e := newTestEngine(b)
	tpl := mustCompile(b, e, buildSource(0, bmLiteralLen*bmSlots, "k"))
	dst := tpl.renderStatic(nil)
	b.ReportAllocs()
	b.SetBytes(int64(len(dst)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dst = tpl.renderStatic(dst)
	}
	sinkBytes = dst
}

func BenchmarkFn_replaceSingle_FreshBuffer(b *testing.B) {
	e := newTestEngine(b)
	tpl := mustCompile(b, e, "prefix-"+buildSource(1, bmLiteralLen, "k"))
	pairs := []string{"value"}
	warm := tpl.replaceSingle(nil, pairs)
	b.ReportAllocs()
	b.SetBytes(int64(len(warm)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkBytes = tpl.replaceSingle(nil, pairs)
	}
}

func BenchmarkFn_replaceSingle_ReusedBuffer(b *testing.B) {
	e := newTestEngine(b)
	tpl := mustCompile(b, e, "prefix-"+buildSource(1, bmLiteralLen, "k"))
	pairs := []string{"value"}
	dst := tpl.replaceSingle(nil, pairs)
	b.ReportAllocs()
	b.SetBytes(int64(len(dst)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dst = tpl.replaceSingle(dst, pairs)
	}
	sinkBytes = dst
}

func BenchmarkFn_replaceSingleMap_FreshBuffer(b *testing.B) {
	e := newTestEngine(b)
	tpl := mustCompile(b, e, "prefix-"+buildSource(1, bmLiteralLen, "k"))
	values := buildValues(1, "k", 24)
	warm := tpl.replaceSingleMap(nil, values)
	b.ReportAllocs()
	b.SetBytes(int64(len(warm)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkBytes = tpl.replaceSingleMap(nil, values)
	}
}

func BenchmarkFn_replaceSingleMap_ReusedBuffer(b *testing.B) {
	e := newTestEngine(b)
	tpl := mustCompile(b, e, "prefix-"+buildSource(1, bmLiteralLen, "k"))
	values := buildValues(1, "k", 24)
	dst := tpl.replaceSingleMap(nil, values)
	b.ReportAllocs()
	b.SetBytes(int64(len(dst)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dst = tpl.replaceSingleMap(dst, values)
	}
	sinkBytes = dst
}

// ---------- render internals ----------

func BenchmarkFn_resolvePairs(b *testing.B) {
	_, tpl, pairs, _ := bmEngineTemplate(b)
	resolved := make([]string, len(tpl.keys))
	found := make([]bool, len(tpl.keys))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		clear(found)
		tpl.resolvePairs(pairs, resolved, found)
	}
	sinkStrings = resolved
}

func BenchmarkFn_resolveMapValues(b *testing.B) {
	_, tpl, _, values := bmEngineTemplate(b)
	resolved := make([]string, len(tpl.keys))
	found := make([]bool, len(tpl.keys))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		clear(found)
		resolveMapValues(tpl.keys, values, resolved, found)
	}
	sinkStrings = resolved
}

func BenchmarkFn_emitParts(b *testing.B) {
	_, tpl, pairs, _ := bmEngineTemplate(b)
	resolved := make([]string, len(tpl.keys))
	found := make([]bool, len(tpl.keys))
	tpl.resolvePairs(pairs, resolved, found)
	parts := make([]string, 0, 2*len(tpl.slots)+1)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkParts, sinkInt = tpl.emitParts(parts[:0], resolved, found)
	}
}

func BenchmarkFn_gatherParts(b *testing.B) {
	_, tpl, pairs, _ := bmEngineTemplate(b)
	resolved := make([]string, len(tpl.keys))
	found := make([]bool, len(tpl.keys))
	tpl.resolvePairs(pairs, resolved, found)
	parts, total := tpl.emitParts(make([]string, 0, 2*len(tpl.slots)+1), resolved, found)
	dst := make([]byte, total)
	b.ReportAllocs()
	b.SetBytes(int64(total))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dst = gatherParts(dst, parts, total)
	}
	sinkBytes = dst
}

func BenchmarkFn_hashPlaceholderKey(b *testing.B) {
	keys := []string{"k0", "user_name", "company_address_line_one", "x"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkU64 = hashPlaceholderKey(keys[i&3])
	}
}

func BenchmarkFn_buildKeyTable(b *testing.B) {
	keys := make([]string, bmSlots)
	for i := range keys {
		keys[i] = "k" + strconv.Itoa(i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkKeyTable = buildKeyTable(keys)
	}
}

func BenchmarkFn_findReplacement(b *testing.B) {
	pairs := buildPairs(bmSlots, "k", 24)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkString, sinkBool = findReplacement(pairs, "k15")
	}
}

func BenchmarkFn_acquireRenderScratch(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s := acquireRenderScratch(64, 129)
		sinkScratch = s
		releaseRenderScratch(s, 64)
	}
}

func BenchmarkFn_releaseRenderScratch(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		s := acquireRenderScratch(64, 129)
		b.StartTimer()
		releaseRenderScratch(s, 64)
	}
}

// ---------- gather primitives ----------

func bmSegs(n int, chunk int) ([]gatherSeg, []byte, int) {
	src := make([]byte, chunk)
	for i := range src {
		src[i] = byte('a' + i%26)
	}
	segs := make([]gatherSeg, n)
	total := 0
	for i := range segs {
		segs[i] = gatherSeg{ptr: unsafe.Pointer(unsafe.SliceData(src)), n: uintptr(chunk)}
		total += chunk
	}
	return segs, make([]byte, total), total
}

func BenchmarkFn_gatherGo(b *testing.B) {
	segs, dst, total := bmSegs(33, 256)
	b.ReportAllocs()
	b.SetBytes(int64(total))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		gatherGo(unsafe.Pointer(unsafe.SliceData(dst)), unsafe.Pointer(unsafe.SliceData(segs)), len(segs))
	}
	sinkBytes = dst
}

func BenchmarkFn_gatherAsm(b *testing.B) {
	segs, dst, total := bmSegs(33, 256)
	b.ReportAllocs()
	b.SetBytes(int64(total))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		gatherAsm(unsafe.Pointer(unsafe.SliceData(dst)), unsafe.Pointer(unsafe.SliceData(segs)), len(segs))
	}
	sinkBytes = dst
}

func BenchmarkFn_runtimeMemmove(b *testing.B) {
	src := make([]byte, 8192)
	dst := make([]byte, 8192)
	b.ReportAllocs()
	b.SetBytes(8192)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runtimeMemmove(unsafe.Pointer(unsafe.SliceData(dst)), unsafe.Pointer(unsafe.SliceData(src)), 8192)
	}
	sinkBytes = dst
}

// ---------- compile path ----------

func BenchmarkFn_compileSource(b *testing.B) {
	e := newTestEngine(b)
	src := bmSource()
	b.ReportAllocs()
	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkTpl, sinkErr = e.compileSource(src)
	}
}

func BenchmarkFn_compileNamedTemplate(b *testing.B) {
	e := newTestEngine(b)
	src := bmSource()
	b.ReportAllocs()
	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkTpl, sinkErr = e.compileNamedTemplate("/tmp/page.alos", "page.alos", src)
	}
}

func BenchmarkFn_scanTemplateDirectory(b *testing.B) {
	_, dir := bmBundleDir(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkFiles, sinkSig, sinkErr = scanTemplateDirectory(dir)
	}
}

func BenchmarkFn_expandBundleSource(b *testing.B) {
	e := newTestEngine(b)
	body := bmSource()
	main := &bundleSourceFile{
		absPath: "/tmp/index.alos", relPath: "index.alos",
		canonical: "index", baseName: "index", fileName: "index.alos",
		raw: `{{include "nav"}}` + body,
	}
	nav := &bundleSourceFile{
		absPath: "/tmp/nav.alos", relPath: "nav.alos",
		canonical: "nav", baseName: "nav", fileName: "nav.alos",
		raw: body,
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		main.expanded, main.expandedOK, nav.expanded, nav.expandedOK = "", false, "", false
		aliases := map[string]*bundleSourceFile{"index": main, "nav": nav}
		sinkString, sinkErr = e.expandBundleSource(main, aliases)
	}
}

func BenchmarkFn_parseIncludeDirective(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkString, sinkBool = parseIncludeDirective(`include "partials/nav"`)
	}
}

func BenchmarkFn_fileSignature(b *testing.B) {
	_, path := bmDirTemplate(b)
	info, err := os.Stat(path)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkSig = fileSignature(info)
	}
}

// ---------- name helpers ----------

func BenchmarkFn_trimTemplateExtension(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkString = trimTemplateExtension("dashboard/adminproducts.alos")
	}
}

func BenchmarkFn_normalizeTemplateName(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkString = normalizeTemplateName("dashboard\\adminproducts.alos")
	}
}

func BenchmarkFn_normalizeTemplateName_AlreadyClean(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkString = normalizeTemplateName("dashboard/adminproducts")
	}
}

func BenchmarkFn_needsNormalizing(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkBool = needsNormalizing("dashboard/adminproducts")
	}
}

func BenchmarkFn_hasTemplateExtension(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkBool = hasTemplateExtension("dashboard/adminproducts.alos")
	}
}
