package core

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/guno1928/alosmap"
)

// Engine manages template loading, compilation, caching, and reload behavior
// for a group of .alos templates.
//
// An Engine owns the delimiter configuration used during parsing, the cache of
// loaded files and bundles, and the optional background reload loop configured
// through auto-refresh. Reuse one Engine for the lifetime of an application or
// for a specific tenant, site, or test scope when you want isolated template
// state.
//
// Delimiter changes affect only templates loaded after the change. Call Stop
// when the Engine is no longer needed so any background refresh goroutine and
// cache resources can be released.
type Engine struct {
	leftDelim      string
	rightDelim     string
	fileCache      *alosmap.TypedMap[string, *parsedFileCacheEntry]
	pool           sync.Pool
	refreshMu      sync.Mutex
	refreshStop    chan struct{}
	autoRefresh    time.Duration
	modifiedOnly   bool
	renderCacheTTL atomic.Int64
}

const DefaultRenderCacheTTL = 4 * time.Second

const RenderCacheDisabled = time.Duration(-1)

// templateCachePreallocChunk is how many cache entry nodes each shard of the
// template cache allocates at a time. Template counts are small, so the batch is
// kept modest.
const templateCachePreallocChunk = 16

type EngineOption func(*Engine)

func WithDelimiters(left, right string) EngineOption {
	return func(e *Engine) {
		if left != "" {
			e.leftDelim = left
		}
		if right != "" {
			e.rightDelim = right
		}
	}
}

func WithAutoRefresh(interval time.Duration) EngineOption {
	return func(e *Engine) {
		e.autoRefresh = interval
	}
}

func WithModifiedOnly(enabled bool) EngineOption {
	return func(e *Engine) {
		e.modifiedOnly = enabled
	}
}

func WithRenderCache(ttl time.Duration) EngineOption {
	return func(e *Engine) {
		e.renderCacheTTL.Store(int64(normalizeRenderCacheTTL(ttl)))
	}
}

func normalizeRenderCacheTTL(ttl time.Duration) time.Duration {
	switch {
	case ttl < 0:
		return 0
	case ttl == 0:
		return DefaultRenderCacheTTL
	default:
		return ttl
	}
}

func (e *Engine) SetRenderCacheTTL(ttl time.Duration) {
	e.renderCacheTTL.Store(int64(normalizeRenderCacheTTL(ttl)))
}

func (e *Engine) RenderCacheTTL() time.Duration {
	return time.Duration(e.renderCacheTTL.Load())
}

func NewEngine(opts ...EngineOption) *Engine {
	e := &Engine{
		leftDelim:  "{{",
		rightDelim: "}}",
		// A typed map keeps cache entries as concrete pointers, so lookups avoid
		// the interface boxing and type assertion the untyped map required.
		// Entry nodes are allocated in batches to reduce churn as templates load.
		fileCache: alosmap.NewTyped[string, *parsedFileCacheEntry]().Prealloc(templateCachePreallocChunk),
	}
	e.renderCacheTTL.Store(int64(DefaultRenderCacheTTL))
	for _, opt := range opts {
		opt(e)
	}
	if e.autoRefresh > 0 {
		e.refreshStop = make(chan struct{})
		go e.autoRefreshLoop(e.refreshStop, e.autoRefresh)
	}
	return e
}

func (e *Engine) autoRefreshLoop(stop chan struct{}, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = e.Reload()
		case <-stop:
			return
		}
	}
}

// Stop releases resources owned by the Engine and stops any background
// auto-refresh loop started for it.
//
// After Stop returns, the Engine should be treated as finished. Templates that
// were already obtained remain ordinary values, but no further automatic
// reloads will occur.
func (e *Engine) Stop() {
	e.refreshMu.Lock()
	if e.refreshStop != nil {
		close(e.refreshStop)
		e.refreshStop = nil
	}
	e.refreshMu.Unlock()
	e.fileCache.Close()
}

// Delimiters reports the left and right delimiter strings currently configured
// on the Engine.
//
// These values are used only when parsing templates during Load. Already-loaded
// templates keep the delimiters they were compiled with.
func (e *Engine) Delimiters() (string, string) {
	return e.leftDelim, e.rightDelim
}

// SetDelimiters updates the delimiter strings the Engine will use for future
// template loads.
//
// Passing an empty string for either side leaves that side unchanged. This
// method does not recompile templates that were already loaded; load or reload
// them again if you want the new delimiters to take effect.
func (e *Engine) SetDelimiters(left, right string) {
	if left != "" {
		e.leftDelim = left
	}
	if right != "" {
		e.rightDelim = right
	}
}

// SetAutoRefresh changes the Engine's automatic reload interval.
//
// Passing 0 disables background reloading. Passing a positive duration
// replaces any existing interval and starts a new background loop that calls
// Reload on templates already loaded by the Engine.
func (e *Engine) SetAutoRefresh(interval time.Duration) {
	e.refreshMu.Lock()
	defer e.refreshMu.Unlock()
	if e.refreshStop != nil {
		close(e.refreshStop)
		e.refreshStop = nil
	}
	e.autoRefresh = interval
	if interval > 0 {
		e.refreshStop = make(chan struct{})
		go e.autoRefreshLoop(e.refreshStop, interval)
	}
}

// AutoRefresh reports the Engine's current automatic reload interval.
//
// A return value of 0 means background reloading is disabled.
func (e *Engine) AutoRefresh() time.Duration {
	return e.autoRefresh
}

// Template is a compiled .alos template ready for repeated rendering.
//
// A Template may represent a single file or a bundle loaded from a directory.
// When it represents a bundle, Named exposes the compiled child templates and
// the default render target is index.alos when present, or the first file in
// sorted order otherwise. Templates are updated in place by Reload so existing
// handles remain valid.
type Template struct {
	engine      *Engine
	sourcePath  string
	loadPath    string
	name        string
	fileName    string
	reloadName  string
	defaultTpl  *Template
	named       map[string]*Template
	names       []string
	literals    []string
	keys        []string
	slots       []slotRef
	table       keyTable
	staticLen   int
	single      singleSlot
	renderCache *alosmap.TypedMap[uint64, *[]byte]
}

func hashRenderValues(values map[string]string) uint64 {
	combined := uint64(len(values))
	for key, value := range values {
		pair := hashPlaceholderKey(key)*0x100000001b3 ^ hashPlaceholderKey(value)
		combined ^= pair * 0x9e3779b97f4a7c15
	}
	return combined
}

func hashRenderPairs(pairs []string) uint64 {
	combined := uint64(len(pairs)) ^ 0x9e3779b97f4a7c15
	for _, item := range pairs {
		combined = (combined ^ hashPlaceholderKey(item)) * 0x100000001b3
	}
	return combined
}

func (tpl *Template) renderCacheTTL() time.Duration {
	if tpl.engine == nil {
		return DefaultRenderCacheTTL
	}
	return time.Duration(tpl.engine.renderCacheTTL.Load())
}

func (tpl *Template) ClearRenderCache() {
	if tpl == nil {
		return
	}
	if store := tpl.renderCache; store != nil {
		store.Clear()
	}
	for _, child := range tpl.named {
		if child != nil && child != tpl {
			if store := child.renderCache; store != nil {
				store.Clear()
			}
		}
	}
}

type slotRef struct {
	keyIndex    int
	placeholder string
}

// keyTableEntry is one open-addressed bucket mapping a placeholder key to its
// index in Template.keys.
type keyTableEntry struct {
	hash uint64
	idx  int32
	used int32
}

// keyTable is a compile-time hash index over a Template's unique keys. It lets
// Replace resolve a flat key/value pair slice in one pass over the pairs rather
// than rescanning every pair once per key.
type keyTable struct {
	mask    uint64
	entries []keyTableEntry
}

type singleSlot struct {
	enabled     bool
	key         string
	prefix      string
	prefixBytes []byte
	prefixPtr   unsafe.Pointer
	prefixLen   uintptr
	suffix      string
	suffixBytes []byte
	suffixPtr   unsafe.Pointer
	suffixLen   uintptr
	placeholder string
	staticLen   int
}

// cacheSignature identifies a cached template's on-disk state. For a single file
// the modification time and size are compared directly, which avoids formatting
// them into a string on every Load. Directories keep an exact listing string so
// that added, removed, or renamed files are always detected.
type cacheSignature struct {
	modNanos int64
	size     int64
	listing  string
}

type parsedFileCacheEntry struct {
	signature cacheSignature
	tpl       *Template
}

// renderScratchPool holds scratch buffers for templates that exceed the inline
// limits. Scratch objects are deliberately not pre-sized: sizing them for the
// largest expected template made every pooled object ~3.2KB and measured 3.5%
// slower through worse cache locality, while steady-state renders already
// allocate nothing.
var renderScratchPool sync.Pool

type renderScratch struct {
	resolved []string
	found    []bool
	parts    []string
}

type bundleSourceFile struct {
	absPath    string
	relPath    string
	modNanos   int64
	size       int64
	canonical  string
	baseName   string
	fileName   string
	raw        string
	expanded   string
	expanding  bool
	expandedOK bool
}

// Load reads and compiles a .alos template from a file path or directory path.
//
// When path points to a file, Load returns a Template for that file. When path
// points to a directory, Load walks the directory tree, collects every .alos
// file, expands explicit include directives such as {{include "nav"}}, and
// returns a bundle Template that can render its default target or expose named
// children through Named.
//
// Load caches compiled results by absolute path and file signature so repeated
// calls for unchanged files reuse existing compiled templates.
func (e *Engine) Load(path string) (*Template, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	return e.loadResolved(abs, false)
}

// Reload re-reads every template or bundle currently loaded by the Engine and
// updates the compiled templates in place.
//
// Existing Template pointers remain valid after Reload. Missing files are
// removed from the cache. If one or more paths fail to reload, Reload returns a
// combined error describing every problem it encountered.
func (e *Engine) Reload() error {
	type pathEntry struct {
		path  string
		entry *parsedFileCacheEntry
	}
	var entries []pathEntry
	e.fileCache.Range(func(path string, entry *parsedFileCacheEntry) bool {
		if path != "" {
			entries = append(entries, pathEntry{path: path, entry: entry})
		}
		return true
	})
	if len(entries) == 0 {
		return nil
	}
	problems := make([]string, 0)
	for _, pe := range entries {
		info, err := os.Stat(pe.path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				e.fileCache.Delete(pe.path)
				continue
			}
			problems = append(problems, fmt.Sprintf("%s: %v", pe.path, err))
			continue
		}
		if pe.entry == nil || pe.entry.tpl == nil {
			continue
		}
		if e.modifiedOnly && !info.IsDir() {
			sig := fileSignature(info)
			if sig == pe.entry.signature {
				continue
			}
		}
		if err := pe.entry.tpl.Reload(); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", pe.path, err))
		}
	}
	if len(problems) != 0 {
		return fmt.Errorf("reload failed: %s", strings.Join(problems, "; "))
	}
	return nil
}

// Reload re-reads this Template from disk and updates it in place.
//
// If the Template came from a directory bundle, the bundle is reloaded and any
// related Named templates already handed out continue to point at the updated
// compiled content. Reload returns an error if the source path can no longer be
// resolved or recompilation fails.
func (tpl *Template) Reload() error {
	if tpl == nil {
		return fmt.Errorf("template is nil")
	}
	if tpl.engine == nil {
		return fmt.Errorf("template has no engine")
	}
	e := tpl.engine
	path := tpl.loadPath
	if path == "" {
		path = tpl.sourcePath
	}
	if path == "" {
		return fmt.Errorf("template has no source path")
	}
	root := tpl
	if entry, ok := e.fileCache.Load(path); ok {
		if entry != nil && entry.tpl != nil {
			root = entry.tpl
		}
	}
	reloaded, err := e.loadResolved(path, true)
	if err != nil {
		return err
	}
	updatedRoot := root
	if updatedRoot == nil {
		updatedRoot = tpl
	}
	applyTemplateReload(updatedRoot, reloaded)
	if entry, ok := e.fileCache.Load(path); ok {
		if entry != nil {
			e.fileCache.Store(path, &parsedFileCacheEntry{signature: entry.signature, tpl: updatedRoot})
		}
	}
	if tpl.reloadName != "" {
		named := updatedRoot.Named(tpl.reloadName)
		if named == nil {
			return fmt.Errorf("reloaded template missing %s", tpl.reloadName)
		}
		applyTemplateReload(tpl, named)
	}
	return nil
}

func applyTemplateReload(dst *Template, src *Template) {
	if dst == nil || src == nil {
		return
	}
	if store := dst.renderCache; store != nil {
		store.Clear()
	}
	dst.sourcePath = src.sourcePath
	dst.loadPath = src.loadPath
	dst.name = src.name
	dst.fileName = src.fileName
	dst.reloadName = src.reloadName
	dst.literals = src.literals
	dst.keys = src.keys
	dst.slots = src.slots
	dst.table = src.table
	dst.staticLen = src.staticLen
	dst.single = src.single
	dst.engine = src.engine
	if src.named == nil {
		dst.defaultTpl = nil
		dst.named = nil
		dst.names = nil
		return
	}
	if dst.named == nil {
		dst.named = make(map[string]*Template, len(src.named))
	}
	childMap := make(map[*Template]*Template, len(src.named))
	updatedNamed := make(map[string]*Template, len(src.named))
	for alias, srcChild := range src.named {
		child := childMap[srcChild]
		if child == nil {
			child = dst.named[alias]
			if child == nil && srcChild.reloadName != "" {
				child = dst.named[normalizeTemplateName(srcChild.reloadName)]
			}
			if child == nil && srcChild.name != "" {
				child = dst.named[normalizeTemplateName(srcChild.name)]
			}
			if child == nil && srcChild.fileName != "" {
				child = dst.named[normalizeTemplateName(srcChild.fileName)]
			}
			if child == nil {
				child = &Template{renderCache: alosmap.NewTyped[uint64, *[]byte]()}
			}
			applyTemplateReload(child, srcChild)
			childMap[srcChild] = child
		}
		updatedNamed[alias] = child
	}
	dst.named = updatedNamed
	dst.names = src.names
	if src.defaultTpl == nil {
		dst.defaultTpl = nil
		return
	}
	if mapped := childMap[src.defaultTpl]; mapped != nil {
		dst.defaultTpl = mapped
		return
	}
	if src.defaultTpl.reloadName != "" {
		dst.defaultTpl = updatedNamed[normalizeTemplateName(src.defaultTpl.reloadName)]
		return
	}
	dst.defaultTpl = nil
}

func (e *Engine) loadResolved(abs string, force bool) (*Template, error) {
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return e.loadDirectory(abs, force)
	}
	return e.loadFile(abs, info, force)
}

// Named returns a child template from a bundle by logical name.
//
// Names are matched case-insensitively and may be provided with or without the
// .alos extension. For single-file templates, Named returns the receiver when
// the requested name matches the template's logical name or file name. Passing
// an empty name returns the default render target for a bundle or the receiver
// for a single-file template. It returns nil when no matching template exists.
func (tpl *Template) Named(name string) *Template {
	if tpl == nil {
		return nil
	}
	if tpl.named != nil {
		if name == "" {
			return tpl.renderTarget()
		}
		return tpl.named[normalizeTemplateName(name)]
	}
	if name == "" {
		return tpl
	}
	normalized := normalizeTemplateName(name)
	if normalized == normalizeTemplateName(tpl.name) || normalized == normalizeTemplateName(tpl.fileName) {
		return tpl
	}
	return nil
}

// Names returns the logical names available from this Template.
//
// For a bundle, the returned slice lists every compiled child template name in
// sorted order. For a single-file template, the slice contains that template's
// own logical name. The returned slice is a copy and can be modified by the
// caller without affecting the Template.
func (tpl *Template) Names() []string {
	if tpl == nil {
		return nil
	}
	if len(tpl.names) != 0 {
		out := make([]string, len(tpl.names))
		copy(out, tpl.names)
		return out
	}
	if tpl.name == "" {
		return nil
	}
	return []string{tpl.name}
}

// Name returns the logical name of the Template's render target.
//
// For bundles, this is the name of the default template that Replace renders
// when given the bundle itself. For single-file templates, it is the template's
// relative path without the .alos extension. A nil receiver returns an empty
// string.
func (tpl *Template) Name() string {
	if tpl == nil {
		return ""
	}
	return tpl.renderTarget().name
}

// FileName returns the source file name of the Template's render target.
//
// For bundles, this is the file name of the default template that Replace
// renders when given the bundle itself. For single-file templates, it is the
// base file name that was loaded. A nil receiver returns an empty string.
func (tpl *Template) FileName() string {
	if tpl == nil {
		return ""
	}
	return tpl.renderTarget().fileName
}

func (tpl *Template) renderTarget() *Template {
	if tpl == nil {
		return nil
	}
	if tpl.defaultTpl != nil {
		return tpl.defaultTpl
	}
	return tpl
}

func (e *Engine) loadFile(abs string, info os.FileInfo, force bool) (*Template, error) {
	signature := fileSignature(info)
	if !force {
		if entry, ok := e.fileCache.Load(abs); ok {
			if entry != nil && entry.signature == signature {
				return entry.tpl, nil
			}
		}
	} else {
		e.fileCache.Delete(abs)
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	tpl, err := e.compileNamedTemplate(abs, filepath.Base(abs), string(raw))
	if err != nil {
		return nil, err
	}
	tpl.loadPath = abs
	e.fileCache.Store(abs, &parsedFileCacheEntry{signature: signature, tpl: tpl})
	return tpl, nil
}

func (e *Engine) loadDirectory(abs string, force bool) (*Template, error) {
	files, signature, err := scanTemplateDirectory(abs)
	if err != nil {
		return nil, err
	}
	if !force {
		if entry, ok := e.fileCache.Load(abs); ok {
			if entry != nil && entry.signature == signature {
				return entry.tpl, nil
			}
		}
	} else {
		e.fileCache.Delete(abs)
	}
	byCanonical := make(map[string]*bundleSourceFile, len(files))
	baseCounts := make(map[string]int, len(files))
	for i := range files {
		files[i].canonical = trimTemplateExtension(filepath.ToSlash(files[i].relPath))
		files[i].baseName = trimTemplateExtension(filepath.Base(files[i].relPath))
		files[i].fileName = filepath.Base(files[i].relPath)
		raw, readErr := os.ReadFile(files[i].absPath)
		if readErr != nil {
			return nil, readErr
		}
		files[i].raw = string(raw)
		byCanonical[normalizeTemplateName(files[i].canonical)] = &files[i]
		baseCounts[normalizeTemplateName(files[i].baseName)]++
	}
	aliases := make(map[string]*bundleSourceFile, len(files)*2)
	for i := range files {
		file := &files[i]
		aliases[normalizeTemplateName(file.canonical)] = file
		if baseCounts[normalizeTemplateName(file.baseName)] == 1 {
			aliases[normalizeTemplateName(file.baseName)] = file
		}
	}
	bundle := &Template{
		renderCache: alosmap.NewTyped[uint64, *[]byte](),
		engine:      e,
		sourcePath:  abs,
		loadPath:    abs,
		named:       make(map[string]*Template, len(aliases)),
		names:       make([]string, 0, len(files)),
	}
	compiled := make(map[string]*Template, len(files))
	for i := range files {
		file := &files[i]
		expanded, expandErr := e.expandBundleSource(file, aliases)
		if expandErr != nil {
			return nil, expandErr
		}
		compiledTpl, compileErr := e.compileNamedTemplate(file.absPath, file.relPath, expanded)
		if compileErr != nil {
			return nil, compileErr
		}
		compiledTpl.loadPath = abs
		compiledTpl.reloadName = file.canonical
		compiled[normalizeTemplateName(file.canonical)] = compiledTpl
		bundle.names = append(bundle.names, file.canonical)
	}
	sort.Strings(bundle.names)
	for alias, file := range aliases {
		bundle.named[alias] = compiled[normalizeTemplateName(file.canonical)]
	}
	bundle.defaultTpl = bundle.named[normalizeTemplateName("index")]
	if bundle.defaultTpl == nil && len(bundle.names) != 0 {
		bundle.defaultTpl = compiled[normalizeTemplateName(bundle.names[0])]
	}
	bundle.reloadName = ""
	e.fileCache.Store(abs, &parsedFileCacheEntry{signature: signature, tpl: bundle})
	return bundle, nil
}

// inlineKeyLimit and inlinePartLimit bound the stack-resident scratch used by
// Replace. Templates larger than this fall back to the pooled scratch.
const (
	inlineKeyLimit  = 16
	inlinePartLimit = 2*inlineKeyLimit + 1
)

// hashPlaceholderKey hashes a placeholder key. Keys are short, so the body reads
// eight bytes at a time and finishes with a single avalanche step.
func hashPlaceholderKey(s string) uint64 {
	n := len(s)
	h := uint64(0x9E3779B97F4A7C15) ^ uint64(n)
	base := unsafe.Pointer(unsafe.StringData(s))
	i := 0
	// Offsets are only turned into pointers when the full read fits inside the
	// string, so no pointer is ever formed past the end of the allocation.
	for ; i+8 <= n; i += 8 {
		h = (h ^ *(*uint64)(unsafe.Add(base, i))) * 0xff51afd7ed558ccd
	}
	if i+4 <= n {
		h = (h ^ uint64(*(*uint32)(unsafe.Add(base, i)))) * 0xff51afd7ed558ccd
		i += 4
	}
	for ; i < n; i++ {
		h = (h ^ uint64(s[i])) * 0xff51afd7ed558ccd
	}
	h ^= h >> 29
	h *= 0xc2b2ae3d27d4eb4f
	h ^= h >> 32
	return h
}

func buildKeyTable(keys []string) keyTable {
	size := uint64(8)
	for size < uint64(len(keys))*2 {
		size <<= 1
	}
	t := keyTable{mask: size - 1, entries: make([]keyTableEntry, size)}
	for i, k := range keys {
		h := hashPlaceholderKey(k)
		pos := h & t.mask
		for t.entries[pos].used != 0 {
			pos = (pos + 1) & t.mask
		}
		t.entries[pos] = keyTableEntry{hash: h, idx: int32(i), used: 1}
	}
	return t
}

// resolvePairs fills resolved and found for every template key from a flat
// key/value pair slice. It preserves the first-occurrence-wins behaviour of
// findReplacement while touching each pair once instead of rescanning the whole
// slice per key.
func (tpl *Template) resolvePairs(pairs []string, resolved []string, found []bool) {
	keys := tpl.keys
	n := len(keys)

	// Callers commonly build pairs in template order. Verifying that costs one
	// compare per key and skips the hash probe entirely when it holds. Template
	// keys are unique, so a positional match cannot hide a duplicate.
	if len(pairs) == n*2 && len(resolved) >= n && len(found) >= n {
		i := 0
		for ; i < n; i++ {
			if pairs[i*2] != keys[i] {
				break
			}
		}
		if i == n {
			resolved = resolved[:n]
			found = found[:n]
			for j := range resolved {
				resolved[j] = pairs[j*2+1]
				found[j] = true
			}
			return
		}
	}

	mask := tpl.table.mask
	entries := tpl.table.entries
	for i := 0; i+1 < len(pairs); i += 2 {
		pk := pairs[i]
		h := hashPlaceholderKey(pk)
		pos := h & mask
		for {
			e := &entries[pos]
			if e.used == 0 {
				break
			}
			if e.hash == h && keys[e.idx] == pk {
				if !found[e.idx] {
					resolved[e.idx] = pairs[i+1]
					found[e.idx] = true
				}
				break
			}
			pos = (pos + 1) & mask
		}
	}
}

// emitParts interleaves literals and per-slot values into parts, returns the
// total output length, and is the single place the render output is laid out.
// parts is appended to from an empty slice with sufficient capacity. Appending
// benchmarked faster than indexed writes here, because it advances a pointer
// instead of bounds-checking every element against the slice length.
func (tpl *Template) emitParts(parts []string, resolved []string, found []bool) ([]string, int) {
	total := tpl.staticLen
	slots := tpl.slots
	literals := tpl.literals
	if len(literals) < len(slots)+1 {
		return parts, total
	}
	literals = literals[:len(slots)+1]
	for i, slot := range slots {
		var value string
		if found[slot.keyIndex] {
			value = resolved[slot.keyIndex]
		} else {
			value = slot.placeholder
		}
		total += len(value)
		parts = append(parts, literals[i], value)
	}
	parts = append(parts, literals[len(slots)])
	return parts, total
}

// gatherParts concatenates parts into a correctly sized dst. A string header is
// laid out exactly like gatherSeg, so parts is handed to the gather directly
// with no per-segment conversion.
const (
	memmoveGatherMinAverage = 384
	memmoveGatherMaxAverage = 4096
)

func gatherParts(dst []byte, parts []string, total int) []byte {
	if cap(dst) < total {
		dst = make([]byte, total)
	} else {
		dst = dst[:total]
	}
	if total == 0 {
		return dst
	}
	base := unsafe.Pointer(unsafe.SliceData(dst))
	segs := unsafe.Pointer(unsafe.SliceData(parts))
	n := len(parts)
	if total >= n*memmoveGatherMinAverage && total < n*memmoveGatherMaxAverage {
		gatherGo(base, segs, n)
		return dst
	}
	gatherAsm(base, segs, n)
	return dst
}

func (tpl *Template) renderStatic(dst []byte) []byte {
	if cap(dst) < tpl.staticLen {
		dst = make([]byte, 0, tpl.staticLen)
	} else {
		dst = dst[:0]
	}
	return append(dst, tpl.literals[0]...)
}

func renderPairsInto(tpl *Template, dst []byte, pairs []string) []byte {
	if tpl.single.enabled {
		return tpl.replaceSingle(dst, pairs)
	}
	if len(tpl.slots) == 0 {
		return tpl.renderStatic(dst)
	}

	nKeys := len(tpl.keys)
	nParts := 2*len(tpl.slots) + 1
	if nKeys <= inlineKeyLimit && nParts <= inlinePartLimit {
		var inlineResolved [inlineKeyLimit]string
		var inlineFound [inlineKeyLimit]bool
		var inlineParts [inlinePartLimit]string
		resolved := inlineResolved[:nKeys]
		found := inlineFound[:nKeys]
		tpl.resolvePairs(pairs, resolved, found)
		parts, total := tpl.emitParts(inlineParts[:0], resolved, found)
		return gatherParts(dst, parts, total)
	}

	pooled := acquireRenderScratch(nKeys, nParts)
	resolved := pooled.resolved[:nKeys]
	found := pooled.found[:nKeys]
	tpl.resolvePairs(pairs, resolved, found)
	parts, total := tpl.emitParts(pooled.parts[:0], resolved, found)
	dst = gatherParts(dst, parts, total)
	releaseRenderScratch(pooled, nKeys)
	return dst
}

func ReplaceMap(tpl *Template, dst []byte, values map[string]string) []byte {
	if tpl == nil {
		return dst[:0]
	}
	tpl = tpl.renderTarget()
	store, ttl := tpl.renderCacheFor(dst)
	if store == nil {
		return renderMapInto(tpl, dst, values)
	}
	key := hashRenderValues(values)
	if hit, ok := store.Load(key); ok && hit != nil {
		return *hit
	}
	out := renderMapInto(tpl, nil, values)
	stored := out
	store.StoreWithTTL(key, &stored, ttl)
	return out
}

func Replace(tpl *Template, dst []byte, pairs []string) []byte {
	if tpl == nil {
		return dst[:0]
	}
	tpl = tpl.renderTarget()
	store, ttl := tpl.renderCacheFor(dst)
	if store == nil {
		return renderPairsInto(tpl, dst, pairs)
	}
	key := hashRenderPairs(pairs)
	if hit, ok := store.Load(key); ok && hit != nil {
		return *hit
	}
	out := renderPairsInto(tpl, nil, pairs)
	stored := out
	store.StoreWithTTL(key, &stored, ttl)
	return out
}

func (tpl *Template) renderCacheFor(dst []byte) (*alosmap.TypedMap[uint64, *[]byte], time.Duration) {
	if dst != nil {
		return nil, 0
	}
	store := tpl.renderCache
	if store == nil {
		return nil, 0
	}
	ttl := tpl.renderCacheTTL()
	if ttl <= 0 {
		return nil, 0
	}
	return store, ttl
}

func renderMapInto(tpl *Template, dst []byte, values map[string]string) []byte {
	if tpl.single.enabled {
		return tpl.replaceSingleMap(dst, values)
	}
	if len(tpl.slots) == 0 {
		return tpl.renderStatic(dst)
	}

	nKeys := len(tpl.keys)
	nParts := 2*len(tpl.slots) + 1
	if nKeys <= inlineKeyLimit && nParts <= inlinePartLimit {
		var inlineResolved [inlineKeyLimit]string
		var inlineFound [inlineKeyLimit]bool
		var inlineParts [inlinePartLimit]string
		resolved := inlineResolved[:nKeys]
		found := inlineFound[:nKeys]
		resolveMapValues(tpl.keys, values, resolved, found)
		parts, total := tpl.emitParts(inlineParts[:0], resolved, found)
		return gatherParts(dst, parts, total)
	}

	pooled := acquireRenderScratch(nKeys, nParts)
	resolved := pooled.resolved[:nKeys]
	found := pooled.found[:nKeys]
	resolveMapValues(tpl.keys, values, resolved, found)
	parts, total := tpl.emitParts(pooled.parts[:0], resolved, found)
	dst = gatherParts(dst, parts, total)
	releaseRenderScratch(pooled, nKeys)
	return dst
}

// resolveMapValues looks up each unique template key once, so a key repeated
// across many slots costs a single map probe.
func resolveMapValues(keys []string, values map[string]string, resolved []string, found []bool) {
	if len(values) == 0 {
		return
	}
	for i, key := range keys {
		if value, ok := values[key]; ok {
			resolved[i] = value
			found[i] = true
		}
	}
}

func (tpl *Template) replaceSingle(dst []byte, pairs []string) []byte {
	if len(pairs) == 1 {
		replacement := pairs[0]
		total := tpl.single.staticLen + len(replacement)
		if cap(dst) < total {
			dst = make([]byte, total)
		} else {
			dst = dst[:total]
		}
		base := unsafe.Pointer(unsafe.SliceData(dst))
		if tpl.single.prefixLen != 0 {
			runtimeMemmove(base, tpl.single.prefixPtr, tpl.single.prefixLen)
		}
		replaceLen := len(replacement)
		if replaceLen != 0 {
			runtimeMemmove(unsafe.Add(base, tpl.single.prefixLen), unsafe.Pointer(unsafe.StringData(replacement)), uintptr(replaceLen))
		}
		if tpl.single.suffixLen != 0 {
			runtimeMemmove(unsafe.Add(base, tpl.single.prefixLen+uintptr(replaceLen)), tpl.single.suffixPtr, tpl.single.suffixLen)
		}
		return dst
	}

	if len(pairs) == 2 && pairs[0] == tpl.single.key {
		replacement := pairs[1]
		total := tpl.single.staticLen + len(replacement)
		if cap(dst) < total {
			dst = make([]byte, total)
		} else {
			dst = dst[:total]
		}
		base := unsafe.Pointer(unsafe.SliceData(dst))
		if tpl.single.prefixLen != 0 {
			runtimeMemmove(base, tpl.single.prefixPtr, tpl.single.prefixLen)
		}
		replaceLen := len(replacement)
		if replaceLen != 0 {
			runtimeMemmove(unsafe.Add(base, tpl.single.prefixLen), unsafe.Pointer(unsafe.StringData(replacement)), uintptr(replaceLen))
		}
		if tpl.single.suffixLen != 0 {
			runtimeMemmove(unsafe.Add(base, tpl.single.prefixLen+uintptr(replaceLen)), tpl.single.suffixPtr, tpl.single.suffixLen)
		}
		return dst
	}

	replacement := tpl.single.placeholder
	if len(pairs) > 2 {
		if value, ok := findReplacement(pairs, tpl.single.key); ok {
			replacement = value
		}
	}
	total := len(tpl.single.prefix) + len(replacement) + len(tpl.single.suffix)
	if cap(dst) < total {
		dst = make([]byte, total)
	} else {
		dst = dst[:total]
	}
	pos := copy(dst, tpl.single.prefixBytes)
	pos += copy(dst[pos:], replacement)
	copy(dst[pos:], tpl.single.suffixBytes)
	return dst
}

func (tpl *Template) replaceSingleMap(dst []byte, values map[string]string) []byte {
	replacement, ok := values[tpl.single.key]
	if !ok {
		replacement = tpl.single.placeholder
	}
	total := len(tpl.single.prefix) + len(replacement) + len(tpl.single.suffix)
	if cap(dst) < total {
		dst = make([]byte, total)
	} else {
		dst = dst[:total]
	}
	base := unsafe.Pointer(unsafe.SliceData(dst))
	if tpl.single.prefixLen != 0 {
		runtimeMemmove(base, tpl.single.prefixPtr, tpl.single.prefixLen)
	}
	replaceLen := len(replacement)
	if replaceLen != 0 {
		runtimeMemmove(unsafe.Add(base, tpl.single.prefixLen), unsafe.Pointer(unsafe.StringData(replacement)), uintptr(replaceLen))
	}
	if tpl.single.suffixLen != 0 {
		runtimeMemmove(unsafe.Add(base, tpl.single.prefixLen+uintptr(replaceLen)), tpl.single.suffixPtr, tpl.single.suffixLen)
	}
	return dst
}

func (e *Engine) compileSource(src string) (*Template, error) {
	leftDelim := e.leftDelim
	rightDelim := e.rightDelim
	leftLen := len(leftDelim)
	rightLen := len(rightDelim)

	literals := make([]string, 0, 8)
	slots := make([]slotRef, 0, 4)
	keys := make([]string, 0, 4)
	keyIndex := make(map[string]int, 4)
	staticLen := 0
	start := 0

	for {
		open := strings.Index(src[start:], leftDelim)
		if open < 0 {
			literal := src[start:]
			literals = append(literals, literal)
			staticLen += len(literal)
			break
		}
		open += start
		close := strings.Index(src[open+leftLen:], rightDelim)
		if close < 0 {
			return nil, errors.New("unterminated placeholder")
		}
		close += open + leftLen

		literal := src[start:open]
		literals = append(literals, literal)
		staticLen += len(literal)

		placeholder := src[open : close+rightLen]
		key := strings.TrimSpace(src[open+leftLen : close])
		if key == "" {
			return nil, errors.New("empty placeholder")
		}
		idx, ok := keyIndex[key]
		if !ok {
			idx = len(keys)
			keyIndex[key] = idx
			keys = append(keys, key)
		}
		slots = append(slots, slotRef{keyIndex: idx, placeholder: placeholder})
		start = close + rightLen
	}

	tpl := &Template{
		renderCache: alosmap.NewTyped[uint64, *[]byte](),
		engine:      e,
		literals:    literals,
		keys:        keys,
		slots:       slots,
		staticLen:   staticLen,
	}
	if len(slots) > 1 {
		tpl.table = buildKeyTable(keys)
	}
	if len(slots) == 1 {
		tpl.single = singleSlot{
			enabled:     true,
			key:         keys[slots[0].keyIndex],
			prefix:      literals[0],
			prefixBytes: []byte(literals[0]),
			prefixPtr:   unsafe.Pointer(unsafe.StringData(literals[0])),
			prefixLen:   uintptr(len(literals[0])),
			suffix:      literals[1],
			suffixBytes: []byte(literals[1]),
			suffixPtr:   unsafe.Pointer(unsafe.StringData(literals[1])),
			suffixLen:   uintptr(len(literals[1])),
			placeholder: slots[0].placeholder,
			staticLen:   len(literals[0]) + len(literals[1]),
		}
	}
	return tpl, nil
}

func (e *Engine) compileNamedTemplate(absPath string, relPath string, src string) (*Template, error) {
	tpl, err := e.compileSource(src)
	if err != nil {
		return nil, err
	}
	tpl.sourcePath = absPath
	tpl.loadPath = absPath
	tpl.fileName = filepath.Base(relPath)
	tpl.name = trimTemplateExtension(filepath.ToSlash(relPath))
	return tpl, nil
}

func acquireRenderScratch(size int, parts int) *renderScratch {
	pooled, _ := renderScratchPool.Get().(*renderScratch)
	if pooled == nil {
		pooled = &renderScratch{}
	}
	if cap(pooled.resolved) < size {
		pooled.resolved = make([]string, size)
	} else {
		pooled.resolved = pooled.resolved[:size]
	}
	if cap(pooled.found) < size {
		pooled.found = make([]bool, size)
	} else {
		pooled.found = pooled.found[:size]
	}
	if cap(pooled.parts) < parts {
		pooled.parts = make([]string, parts)
	} else {
		pooled.parts = pooled.parts[:parts]
	}
	return pooled
}

func releaseRenderScratch(pooled *renderScratch, size int) {
	if pooled == nil {
		return
	}
	clear(pooled.resolved[:size])
	clear(pooled.found[:size])
	// parts holds string headers for the render just finished; clearing it stops
	// the pool from pinning those strings alive until the next render.
	clear(pooled.parts)
	renderScratchPool.Put(pooled)
}

func scanTemplateDirectory(root string) ([]bundleSourceFile, cacheSignature, error) {
	files := make([]bundleSourceFile, 0, 8)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".alos") {
			return nil
		}
		// WalkDir already carries the directory entry's metadata, so reuse it
		// instead of issuing a second stat syscall per file below.
		info, err := d.Info()
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, bundleSourceFile{
			absPath:  path,
			relPath:  relPath,
			modNanos: info.ModTime().UnixNano(),
			size:     info.Size(),
		})
		return nil
	})
	if err != nil {
		return nil, cacheSignature{}, err
	}
	if len(files) == 0 {
		return nil, cacheSignature{}, fmt.Errorf("no .alos files found in %s", root)
	}
	sort.Slice(files, func(i, j int) bool {
		return filepath.ToSlash(files[i].relPath) < filepath.ToSlash(files[j].relPath)
	})
	listing := make([]byte, 0, len(files)*48)
	for i := range files {
		listing = append(listing, filepath.ToSlash(files[i].relPath)...)
		listing = append(listing, '|')
		listing = strconv.AppendInt(listing, files[i].modNanos, 10)
		listing = append(listing, '|')
		listing = strconv.AppendInt(listing, files[i].size, 10)
		listing = append(listing, ';')
	}
	return files, cacheSignature{listing: string(listing)}, nil
}

func (e *Engine) expandBundleSource(file *bundleSourceFile, aliases map[string]*bundleSourceFile) (string, error) {
	if file.expandedOK {
		return file.expanded, nil
	}
	if file.expanding {
		return "", fmt.Errorf("template include cycle involving %s", file.relPath)
	}
	file.expanding = true
	defer func() {
		file.expanding = false
	}()

	leftDelim := e.leftDelim
	rightDelim := e.rightDelim
	leftLen := len(leftDelim)
	rightLen := len(rightDelim)

	var out strings.Builder
	src := file.raw
	start := 0
	for {
		open := strings.Index(src[start:], leftDelim)
		if open < 0 {
			out.WriteString(src[start:])
			break
		}
		open += start
		close := strings.Index(src[open+leftLen:], rightDelim)
		if close < 0 {
			return "", errors.New("unterminated placeholder")
		}
		close += open + leftLen
		out.WriteString(src[start:open])

		inner := strings.TrimSpace(src[open+leftLen : close])

		if includeName, ok := parseIncludeDirective(inner); ok {
			if included := aliases[normalizeTemplateName(includeName)]; included != nil {
				expanded, err := e.expandBundleSource(included, aliases)
				if err != nil {
					return "", err
				}
				out.WriteString(expanded)
			} else {
				out.WriteString(src[open : close+rightLen])
			}
		} else {
			out.WriteString(src[open : close+rightLen])
		}
		start = close + rightLen
	}
	file.expanded = out.String()
	file.expandedOK = true
	return file.expanded, nil
}

func parseIncludeDirective(inner string) (string, bool) {
	if !strings.HasPrefix(inner, "include ") {
		return "", false
	}
	arg := strings.TrimSpace(inner[len("include "):])
	if len(arg) < 2 {
		return "", false
	}
	quote := arg[0]
	if quote != '"' && quote != '\'' {
		return "", false
	}
	end := strings.IndexByte(arg[1:], quote)
	if end < 0 {
		return "", false
	}
	return arg[1 : 1+end], true
}

func fileSignature(info os.FileInfo) cacheSignature {
	return cacheSignature{modNanos: info.ModTime().UnixNano(), size: info.Size()}
}

func trimTemplateExtension(name string) string {
	clean := filepath.ToSlash(strings.TrimSpace(name))
	if hasTemplateExtension(clean) {
		return clean[:len(clean)-len(".alos")]
	}
	return clean
}

func normalizeTemplateName(name string) string {
	// Lookups usually pass an already-normalised name, so scan first and only
	// allocate when the string actually needs rewriting.
	if !needsNormalizing(name) {
		return name
	}
	return strings.ToLower(trimTemplateExtension(name))
}

func needsNormalizing(name string) bool {
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c == '\\' || c == ' ' || c == '\t' || c == '\n' || c == '\r' || (c >= 'A' && c <= 'Z') {
			return true
		}
	}
	return hasTemplateExtension(name)
}

func hasTemplateExtension(name string) bool {
	const ext = ".alos"
	if len(name) < len(ext) {
		return false
	}
	return strings.EqualFold(name[len(name)-len(ext):], ext)
}

func findReplacement(pairs []string, key string) (string, bool) {
	if len(pairs) == 2 {
		if pairs[0] == key {
			return pairs[1], true
		}
		return "", false
	}
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i] == key {
			return pairs[i+1], true
		}
	}
	return "", false
}
