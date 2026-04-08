package alos

import (
	"fmt"
	"time"

	corepkg "github.com/guno1928/alostemplate/core"
)

type Template = corepkg.Template

type Engine = corepkg.Engine

type Option = corepkg.EngineOption

// WithDelimiters returns an Option that changes the delimiter strings an Engine
// uses when it parses placeholders and include directives during Load.
//
// The default delimiters are "{{" and "}}". Custom delimiters are useful
// when .alos files live beside another template language or markup syntax that
// already uses curly braces. The configured delimiters apply only to templates
// loaded after the Engine is created or reconfigured. Templates that were
// already compiled keep the delimiters they were parsed with.
func WithDelimiters(left, right string) Option { return corepkg.WithDelimiters(left, right) }

// WithAutoRefresh returns an Option that enables periodic reload of templates
// loaded by a newly created Engine.
//
// The default interval is 0, which disables background reloading. Passing a
// positive duration starts a background loop for that Engine and calls Reload
// on every tick. This is most useful in long-running processes that should pick
// up on-disk template edits without rebuilding template handles.
func WithAutoRefresh(interval time.Duration) Option { return corepkg.WithAutoRefresh(interval) }

// WithModifiedOnly returns an Option that makes Engine reload passes skip files
// whose on-disk signature has not changed.
//
// The default is false, which causes reload passes to re-read every loaded
// path. When this option is enabled, reload work is reduced by comparing each
// file's modification time and size before recompiling it.
func WithModifiedOnly(enabled bool) Option { return corepkg.WithModifiedOnly(enabled) }

// New constructs an isolated Engine with its own template cache, delimiter
// configuration, and optional auto-refresh loop.
//
// Use a custom Engine when you want settings that differ from the package-level
// defaults or when you want template state separated by application, tenant, or
// test scope. Call Stop when the Engine is no longer needed.
//
// Example:
//
//	e := alos.New(
//	    alos.WithDelimiters("<%", "%>"),
//	    alos.WithAutoRefresh(30*time.Second),
//	    alos.WithModifiedOnly(true),
//	)
//	defer e.Stop()
func New(opts ...Option) *Engine {
	return corepkg.NewEngine(opts...)
}

var defaultEngine = corepkg.NewEngine()

// Load reads and compiles a .alos file or directory using the package-level
// default Engine.
//
// A file path returns a single Template. A directory path returns a bundle that
// contains every .alos file found under that directory, expands explicit
// include directives, and renders index.alos by default when it exists.
func Load(path string) (*Template, error) {
	return defaultEngine.Load(path)
}

// Reload tells the package-level default Engine to re-read every template it
// has already loaded and update those compiled templates in place.
//
// Existing Template pointers remain valid after Reload. Use it when you want
// to pick up on-disk file changes without rebuilding template references.
func Reload() error {
	return defaultEngine.Reload()
}

// SetDelimiters changes the delimiters the package-level default Engine will
// use for future Load calls.
//
// This does not retroactively reparse templates that were already loaded. Pass
// non-empty strings to change the left and right delimiters; an empty string
// leaves that side unchanged.
func SetDelimiters(left, right string) {
	defaultEngine.SetDelimiters(left, right)
}

// SetAutoRefresh changes the background reload interval for the package-level
// default Engine.
//
// Passing 0 disables automatic reloads. Passing a positive duration starts or
// replaces the current interval so already-loaded templates are periodically
// refreshed from disk.
func SetAutoRefresh(interval time.Duration) {
	defaultEngine.SetAutoRefresh(interval)
}

// Delimiters reports the delimiter strings currently configured on the
// package-level default Engine.
func Delimiters() (string, string) {
	return defaultEngine.Delimiters()
}

// Stop releases resources owned by the package-level default Engine and stops
// any background auto-refresh loop associated with it.
//
// Call this when your process or test no longer needs the default Engine.
func Stop() {
	defaultEngine.Stop()
}

// Replace renders tpl into dst using one of the supported replacement formats.
//
// The values argument may be a string, a []string of flat key/value pairs, or
// a map[string]string. A single string is shorthand for templates with one
// placeholder. A []string with more than one element must contain an even
// number of items. Missing keys are left in the output as their original
// placeholder text, which makes unresolved placeholders visible.
//
// Pass nil for dst in the normal case. Replace returns an error if tpl is nil
// or if values has an unsupported type.
func Replace(tpl *Template, dst []byte, values any) ([]byte, error) {
	if tpl == nil {
		return nil, fmt.Errorf("template is nil")
	}
	switch typed := values.(type) {
	case string:
		return corepkg.Replace(tpl, dst, []string{typed}), nil
	case []string:
		if len(typed) == 0 {
			return corepkg.Replace(tpl, dst, nil), nil
		}
		if len(typed) > 1 && len(typed)%2 != 0 {
			return nil, fmt.Errorf("replacement pairs must have an even number of items or a single value shorthand")
		}
		return corepkg.Replace(tpl, dst, typed), nil
	case map[string]string:
		return corepkg.ReplaceMap(tpl, dst, typed), nil
	default:
		return nil, fmt.Errorf("unsupported replacement input %T: use string, []string, or map[string]string", values)
	}
}
