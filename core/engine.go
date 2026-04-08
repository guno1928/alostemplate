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
	leftDelim    string
	rightDelim   string
	fileCache    *alosmap.Map
	pool         sync.Pool
	refreshMu    sync.Mutex
	refreshStop  chan struct{}
	autoRefresh  time.Duration
	modifiedOnly bool
}

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

func NewEngine(opts ...EngineOption) *Engine {
	e := &Engine{
		leftDelim:  "{{",
		rightDelim: "}}",
		fileCache:  alosmap.New(),
	}
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
	engine     *Engine
	sourcePath string
	loadPath   string
	name       string
	fileName   string
	reloadName string
	defaultTpl *Template
	named      map[string]*Template
	names      []string
	literals   []string
	keys       []string
	slots      []slotRef
	staticLen  int
	single     singleSlot
}

type slotRef struct {
	keyIndex    int
	placeholder string
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

type parsedFileCacheEntry struct {
	signature string
	tpl       *Template
}

var renderScratchPool sync.Pool

type renderScratch struct {
	resolved []string
	found    []bool
}

type bundleSourceFile struct {
	absPath    string
	relPath    string
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
	e.fileCache.Range(func(key alosmap.Key, value any) bool {
		path := key.StringVal()
		if path != "" {
			entry, _ := value.(*parsedFileCacheEntry)
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
				e.fileCache.Delete(alosmap.S(pe.path))
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
	if cached, ok := e.fileCache.Load(alosmap.S(path)); ok {
		entry, _ := cached.(*parsedFileCacheEntry)
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
	if cached, ok := e.fileCache.Load(alosmap.S(path)); ok {
		entry, _ := cached.(*parsedFileCacheEntry)
		if entry != nil {
			e.fileCache.Store(alosmap.S(path), &parsedFileCacheEntry{signature: entry.signature, tpl: updatedRoot})
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
	dst.sourcePath = src.sourcePath
	dst.loadPath = src.loadPath
	dst.name = src.name
	dst.fileName = src.fileName
	dst.reloadName = src.reloadName
	dst.literals = src.literals
	dst.keys = src.keys
	dst.slots = src.slots
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
				child = &Template{}
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
		if cached, ok := e.fileCache.Load(alosmap.S(abs)); ok {
			entry, _ := cached.(*parsedFileCacheEntry)
			if entry != nil && entry.signature == signature {
				return entry.tpl, nil
			}
		}
	} else {
		e.fileCache.Delete(alosmap.S(abs))
	}
	if cached, ok := e.fileCache.Load(alosmap.S(abs)); ok {
		entry, _ := cached.(*parsedFileCacheEntry)
		if entry != nil && entry.signature == signature {
			return entry.tpl, nil
		}
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
	e.fileCache.Store(alosmap.S(abs), &parsedFileCacheEntry{signature: signature, tpl: tpl})
	return tpl, nil
}

func (e *Engine) loadDirectory(abs string, force bool) (*Template, error) {
	files, signature, err := scanTemplateDirectory(abs)
	if err != nil {
		return nil, err
	}
	if !force {
		if cached, ok := e.fileCache.Load(alosmap.S(abs)); ok {
			entry, _ := cached.(*parsedFileCacheEntry)
			if entry != nil && entry.signature == signature {
				return entry.tpl, nil
			}
		}
	} else {
		e.fileCache.Delete(alosmap.S(abs))
	}
	if cached, ok := e.fileCache.Load(alosmap.S(abs)); ok {
		entry, _ := cached.(*parsedFileCacheEntry)
		if entry != nil && entry.signature == signature {
			return entry.tpl, nil
		}
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
		engine:     e,
		sourcePath: abs,
		loadPath:   abs,
		named:      make(map[string]*Template, len(aliases)),
		names:      make([]string, 0, len(files)),
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
	e.fileCache.Store(alosmap.S(abs), &parsedFileCacheEntry{signature: signature, tpl: bundle})
	return bundle, nil
}

func Replace(tpl *Template, dst []byte, pairs []string) []byte {
	if tpl == nil {
		return dst[:0]
	}
	tpl = tpl.renderTarget()
	if tpl.single.enabled {
		return tpl.replaceSingle(dst, pairs)
	}
	if len(tpl.slots) == 0 {
		if cap(dst) < tpl.staticLen {
			dst = make([]byte, 0, tpl.staticLen)
		} else {
			dst = dst[:0]
		}
		return append(dst, tpl.literals[0]...)
	}

	var inlineResolved [8]string
	var inlineFound [8]bool
	resolved := inlineResolved[:0]
	found := inlineFound[:0]
	var pooled *renderScratch
	if len(tpl.keys) <= len(inlineResolved) {
		resolved = inlineResolved[:len(tpl.keys)]
		found = inlineFound[:len(tpl.keys)]
	} else {
		pooled = acquireRenderScratch(len(tpl.keys))
		defer releaseRenderScratch(pooled, len(tpl.keys))
		resolved = pooled.resolved[:len(tpl.keys)]
		found = pooled.found[:len(tpl.keys)]
	}
	for i, key := range tpl.keys {
		value, ok := findReplacement(pairs, key)
		if ok {
			resolved[i] = value
			found[i] = true
		}
	}

	total := tpl.staticLen
	for _, slot := range tpl.slots {
		if found[slot.keyIndex] {
			total += len(resolved[slot.keyIndex])
		} else {
			total += len(slot.placeholder)
		}
	}

	if cap(dst) < total {
		dst = make([]byte, total)
	} else {
		dst = dst[:total]
	}
	pos := 0
	for i, slot := range tpl.slots {
		pos += copy(dst[pos:], tpl.literals[i])
		if found[slot.keyIndex] {
			pos += copy(dst[pos:], resolved[slot.keyIndex])
		} else {
			pos += copy(dst[pos:], slot.placeholder)
		}
	}
	copy(dst[pos:], tpl.literals[len(tpl.literals)-1])
	return dst
}

func ReplaceMap(tpl *Template, dst []byte, values map[string]string) []byte {
	if tpl == nil {
		return dst[:0]
	}
	tpl = tpl.renderTarget()
	if tpl.single.enabled {
		return tpl.replaceSingleMap(dst, values)
	}
	if len(tpl.slots) == 0 {
		if cap(dst) < tpl.staticLen {
			dst = make([]byte, 0, tpl.staticLen)
		} else {
			dst = dst[:0]
		}
		return append(dst, tpl.literals[0]...)
	}
	if len(tpl.slots) <= 4 {
		return tpl.replaceMapSmall(dst, values)
	}

	var inlineResolved [8]string
	var inlineFound [8]bool
	resolved := inlineResolved[:0]
	found := inlineFound[:0]
	var pooled *renderScratch
	if len(tpl.keys) <= len(inlineResolved) {
		resolved = inlineResolved[:len(tpl.keys)]
		found = inlineFound[:len(tpl.keys)]
	} else {
		pooled = acquireRenderScratch(len(tpl.keys))
		defer releaseRenderScratch(pooled, len(tpl.keys))
		resolved = pooled.resolved[:len(tpl.keys)]
		found = pooled.found[:len(tpl.keys)]
	}
	for i, key := range tpl.keys {
		value, ok := values[key]
		if ok {
			resolved[i] = value
			found[i] = true
		}
	}

	total := tpl.staticLen
	for _, slot := range tpl.slots {
		if found[slot.keyIndex] {
			total += len(resolved[slot.keyIndex])
		} else {
			total += len(slot.placeholder)
		}
	}

	if cap(dst) < total {
		dst = make([]byte, total)
	} else {
		dst = dst[:total]
	}
	pos := 0
	for i, slot := range tpl.slots {
		pos += copy(dst[pos:], tpl.literals[i])
		if found[slot.keyIndex] {
			pos += copy(dst[pos:], resolved[slot.keyIndex])
		} else {
			pos += copy(dst[pos:], slot.placeholder)
		}
	}
	copy(dst[pos:], tpl.literals[len(tpl.literals)-1])
	return dst
}

func (tpl *Template) replaceMapSmall(dst []byte, values map[string]string) []byte {
	var resolved [4]string
	var found [4]bool
	total := tpl.staticLen
	for i, slot := range tpl.slots {
		value, ok := values[tpl.keys[slot.keyIndex]]
		if ok {
			resolved[i] = value
			found[i] = true
			total += len(value)
		} else {
			total += len(slot.placeholder)
		}
	}
	if cap(dst) < total {
		dst = make([]byte, total)
	} else {
		dst = dst[:total]
	}
	pos := 0
	for i, slot := range tpl.slots {
		pos += copy(dst[pos:], tpl.literals[i])
		if found[i] {
			pos += copy(dst[pos:], resolved[i])
		} else {
			pos += copy(dst[pos:], slot.placeholder)
		}
	}
	copy(dst[pos:], tpl.literals[len(tpl.literals)-1])
	return dst
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
		engine:    e,
		literals:  literals,
		keys:      keys,
		slots:     slots,
		staticLen: staticLen,
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

func acquireRenderScratch(size int) *renderScratch {
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
	return pooled
}

func releaseRenderScratch(pooled *renderScratch, size int) {
	if pooled == nil {
		return
	}
	clear(pooled.resolved[:size])
	renderScratchPool.Put(pooled)
}

func scanTemplateDirectory(root string) ([]bundleSourceFile, string, error) {
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
		info, err := d.Info()
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, bundleSourceFile{absPath: path, relPath: relPath})
		_ = info
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	if len(files) == 0 {
		return nil, "", fmt.Errorf("no .alos files found in %s", root)
	}
	sort.Slice(files, func(i, j int) bool {
		return filepath.ToSlash(files[i].relPath) < filepath.ToSlash(files[j].relPath)
	})
	var signature strings.Builder
	for i := range files {
		info, err := os.Stat(files[i].absPath)
		if err != nil {
			return nil, "", err
		}
		signature.WriteString(filepath.ToSlash(files[i].relPath))
		signature.WriteByte('|')
		signature.WriteString(strconv.FormatInt(info.ModTime().UnixNano(), 10))
		signature.WriteByte('|')
		signature.WriteString(strconv.FormatInt(info.Size(), 10))
		signature.WriteByte(';')
	}
	return files, signature.String(), nil
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

func fileSignature(info os.FileInfo) string {
	return strconv.FormatInt(info.ModTime().UnixNano(), 10) + ":" + strconv.FormatInt(info.Size(), 10)
}

func trimTemplateExtension(name string) string {
	clean := filepath.ToSlash(strings.TrimSpace(name))
	if strings.HasSuffix(strings.ToLower(clean), ".alos") {
		return clean[:len(clean)-len(".alos")]
	}
	return clean
}

func normalizeTemplateName(name string) string {
	return strings.ToLower(trimTemplateExtension(name))
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
