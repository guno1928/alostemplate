# alostemplate

A fast, configurable `.alos` template engine for Go. Load template files once, replace placeholders at high speed, and optionally auto-refresh from disk on a timer.

Features:

- **Configurable delimiters** — use `{{` `}}`, `<%` `%>`, `[%` `%]`, or anything you want
- **Explicit includes** — `{{include "nav"}}` pulls one file into another at load time
- **Auto-refresh** — set an interval and templates reload from disk automatically
- **Render cache, on by default** — repeat renders of the same template and values are served from a 4-second cache in about 7–10 ns for a one-value page, with zero allocations
- **Returns a `string`** — immutable, so cached pages are shared safely with no copy and no read-only caveats
- **Zero-allocation rendering** — `AppendValues` and `AppendReplace` render into a buffer you reuse
- **Values without keys** — `ReplaceValues` and `AppendValues` take values in `tpl.Keys()` order and skip key lookup entirely
- **[alosmap](https://github.com/guno1928/alosmap)** typed map for the concurrent file cache

## Install

```bash
go get github.com/guno1928/alostemplate
```

## Import

```go
import alos "github.com/guno1928/alostemplate"
```

## Quick Start

```go
tpl, err := alos.Load("page.alos")
if err != nil {
    panic(err)
}
out, err := alos.Replace(tpl, map[string]string{
    "title": "Hello World",
})
if err != nil {
    panic(err)
}
fmt.Println(out)
```

## Public API

### Package-Level (Default Engine)

| Function | Description |
|---|---|
| `Load(path)` | Load a single `.alos` file or a directory bundle |
| `Reload()` | Force re-read all loaded templates from disk |
| `Replace(tpl, values)` | Render a template with placeholder values |
| `AppendReplace(dst, tpl, values)` | Render onto the end of `dst` and return it; no cache, no allocation once `dst` is large enough |
| `ReplaceValues(tpl, values)` | Render with a `[]string` of values in `tpl.Keys()` order; cached like `Replace`, no key lookup, no error return |
| `AppendValues(dst, tpl, values)` | Same values, rendered onto the end of `dst`; no cache, no allocation once `dst` is large enough |
| `SetDelimiters(left, right)` | Change delimiters for future loads |
| `SetAutoRefresh(interval)` | Enable/disable periodic auto-reload (0 = off) |
| `SetRenderCacheTTL(ttl)` | Change how long rendered output is cached |
| `RenderCacheTTL()` | Current render cache lifetime (0 = disabled) |
| `Delimiters()` | Returns current left and right delimiters |
| `Stop()` | Stop the auto-refresh goroutine |

### Custom Engine

| Function | Description |
|---|---|
| `New(opts...)` | Create a new engine with options |
| `engine.Load(path)` | Load a template |
| `engine.Reload()` | Reload all loaded templates |
| `engine.SetDelimiters(l, r)` | Change delimiters at runtime |
| `engine.SetAutoRefresh(d)` | Change auto-refresh interval at runtime |
| `engine.SetRenderCacheTTL(d)` | Change render cache lifetime at runtime |
| `engine.RenderCacheTTL()` | Get current render cache lifetime |
| `engine.Delimiters()` | Get current delimiters |
| `engine.AutoRefresh()` | Get current auto-refresh interval |
| `engine.Stop()` | Stop engine and release resources |

### Options

| Option | Default | Description |
|---|---|---|
| `WithDelimiters(l, r)` | `{{`, `}}` | Set placeholder delimiters |
| `WithAutoRefresh(d)` | `0` (off) | Auto-reload interval |
| `WithModifiedOnly(bool)` | `false` | Only reload files whose on-disk signature changed |
| `WithRenderCache(d)` | `4s` | Render cache lifetime. `0` selects the default; `RenderCacheDisabled` turns it off |

### Template Methods

| Method | Description |
|---|---|
| `tpl.Named(name)` | Get a child template from a bundle by name |
| `tpl.Names()` | List all template names in a bundle |
| `tpl.Name()` | Template's logical name (path without `.alos` extension) |
| `tpl.FileName()` | Template's source file name |
| `tpl.Reload()` | Reload this specific template from disk |
| `tpl.ClearRenderCache()` | Drop every cached render for this template |
| `tpl.Keys()` | The template's placeholder keys, each once, in order of first appearance — the order `ReplaceValues` and `AppendValues` expect |

### Replace Inputs

`Replace(tpl, values)` returns the finished page as a `string` and accepts four value types:

- **`nil`** — render with no substitutions
- **`string`** — single-value shorthand (fills the first placeholder)
- **`[]string`** — flat `"key","value","key","value"` pairs
- **`map[string]string`** — key→value map (recommended)

## Includes

Inside a directory bundle, use the `include` directive to inline one file into another **at load time**:

```
{{include "nav"}}
{{include 'footer'}}
```

Both double and single quotes work. The name matches by file stem (without `.alos` extension). Include expansion happens once at load time — the included content becomes part of the compiled template, so there is no per-render overhead.

**Bare placeholders are NOT auto-expanded to file names.** Writing `{{nav}}` does not pull in `nav.alos`. It stays as a normal placeholder named `nav` that you must supply a value for through `Replace`. If you want file inclusion, you must write `{{include "nav"}}`.

Chained includes work: if `a.alos` includes `b.alos` and `b.alos` includes `c.alos`, all three are expanded. Circular includes are detected and produce an error.

Include works with any configured delimiters:

```
<%include "nav"%>
[%include "footer"%]
```

## How Loading Works

### Single File

`Load("page.alos")` reads the file, parses it for placeholders using the configured delimiters, and compiles it into a `Template`. The result is cached in an `alosmap` by absolute path + file signature (mod time + size). Subsequent `Load` calls for the same file return the cached template if the file has not changed.

### Directory Bundle

`Load("templates")` walks the directory tree, collects every `.alos` file, expands any `include` directives, and compiles each file into a `Template`. All templates are stored in a bundle. The bundle exposes them via `Named(name)`.

If the directory contains `index.alos`, it becomes the default render target — calling `Replace(bundle, ...)` renders `index.alos`. If there is no `index.alos`, the first file alphabetically is used.

Template names inside a bundle are the relative path with the `.alos` extension stripped. A file at `templates/nav.alos` has name `nav`. A file at `templates/partials/sidebar.alos` has name `partials/sidebar`. When names are unique at the basename level, you can also look them up by just the basename (e.g. `bundle.Named("sidebar")`).

## How Replace Works

`Replace` takes a compiled template and a set of values. It resolves each placeholder key, then concatenates literals and values into the finished page.

- If a placeholder key is found in the values, the value is substituted.
- If a placeholder key is **not** found, the original placeholder text (e.g. `{{title}}`) is left in the output unchanged. This makes missing keys visible during development.

### Rendering is cached and returns a string

`Replace` returns a `string`, and results are served from the render cache:

| | Cost (128 KB page, one value) | Allocations |
|---|---|---|
| Cache hit | **~7 ns** | **0** |
| Not in the cache (renders the page) | ~8 µs | 1, or 2 when the page is stored |

```go
out, err := alos.Replace(tpl, values)
io.WriteString(w, out)
```

Because the result is a `string`, it is immutable. The engine can hand the same
page to every caller with equal values at no cost, and there is nothing a caller
can do to corrupt it.

### Rendering into your own buffer

`AppendReplace` renders onto the end of a `[]byte` and returns it. It skips the
cache and allocates nothing once the buffer is big enough, so it is the right
call for pages whose values are different on every request.

```go
buf = buf[:0]
buf, err = alos.AppendReplace(buf, tpl, values)
w.Write(buf)
```

It accepts the same value types as `Replace` and produces the same bytes.

### Values without keys

`ReplaceValues` and `AppendValues` take only the values, in the order `tpl.Keys()`
reports: each distinct key once, in order of first appearance. Nothing is looked
up by name, so these are the fastest calls in the package.

```go
keys := tpl.Keys() // e.g. ["title", "body"]

out := alos.ReplaceValues(tpl, []string{"Hello", "World"})          // cached
buf = alos.AppendValues(buf[:0], tpl, []string{"Hello", "World"}) // no cache, no allocation
```

If the list is shorter than `tpl.Keys()`, the remaining placeholders are left in
the output as written. Extra values are ignored. A nil template renders nothing.
Neither call returns an error.

### Fast paths

- **No placeholders** — the template text is returned directly with no copy.
- **Values in key order** (`ReplaceValues`, `AppendValues`) — the output size is added up, then literals and values are copied straight into the output in one pass. No keys are read.
- **Pairs supplied in template order** — keys are checked in place, then the same single pass runs.
- **A map, or a template that repeats a key** — the value for each placeholder is collected into a small stack array (a pooled scratch buffer past 16 placeholders), then the same single pass runs.
- **Pairs in any other order** — each template compiles an open-addressed hash index over its keys, so pairs are resolved in one pass. Templates past the inline limits use a pooled scratch buffer, still with no per-render allocation in steady state.
- **The pass itself** — on `amd64` it is hand-written Plan 9 assembly (`gather_amd64.s`) that sizes the output, checks it fits and copies every segment, using AVX2 for segments over 64 bytes when the CPU has it. Other platforms and the `purego` build tag use a pure-Go version.
- **Appending never writes past the result** — bytes in `dst` beyond the returned slice are left untouched, and a buffer that has to grow is replaced, not modified.
- **New strings are not zeroed first** — the output is allocated uninitialised and then completely overwritten.

## Render Cache

Every template carries its own render cache, keyed on a hash of the values you pass. It is **enabled by default with a 4-second TTL**.

```go
// Default: cached for 4 seconds
tpl, _ := alos.Load("page.alos")
out, _ := alos.Replace(tpl, values)   // ~9 ns on a hit for one value passed as pairs, 0 allocations

// Longer TTL for pages that rarely change
e := alos.New(alos.WithRenderCache(30 * time.Second))

// Off entirely
e := alos.New(alos.WithRenderCache(alos.RenderCacheDisabled))

// Drop one template's cached output immediately
tpl.ClearRenderCache()
```

What you should know before relying on it:

- **The cache is lazy, not refreshed in the background.** After the TTL expires, the next caller to arrive pays the full render cost synchronously. There is no background goroutine.
- **Output can be up to the TTL stale.** If the values behind a page change, callers keep seeing the previous render until the entry expires.
- **`Reload()` invalidates it.** A reloaded template starts with an empty cache, so on-disk edits are never masked by the cache.
- **Memory is bounded.** Each template keeps at most 512 rendered pages. A new page replaces an expired one, or the older of two candidates when both are still live. Expired pages are released when their slot is reused or the cache is cleared, not on a timer.
- **A page is stored the second time it is rendered.** The first render of a set of values only notes that it was seen; the page is kept when the same values arrive again. Pages rendered once are never held in memory. The note is a 512-entry table per template, so under heavy churn a page can need more than two renders before it is stored.
- **High-cardinality pages gain nothing.** A page rendered with a different value every time (a user ID, a session token) never hits and still pays for hashing its values. Use `AppendValues` or `AppendReplace` for those, or disable the cache on that Engine.
- **TTL resolution is about 1 ms.** Expiry is checked against a cached clock.
- **Keys are hashed with per-process random secrets.** Two different value sets could in principle collide on a 64-bit key, but the key cannot be predicted or collided from outside the process.

## Reload

`Reload()` walks everything already loaded by the engine, re-reads files from disk, and swaps in freshly compiled templates. Existing template handles (pointers returned by `Load` or `Named`) remain valid after reload and render the new content. This means you can hold onto a `*Template` at server startup and it will reflect the latest file content after any reload.

`tpl.Reload()` reloads a single template (or bundle) rather than all loaded templates.

`SetAutoRefresh(interval)` starts a background goroutine that calls `Reload()` on every tick. Pass 0 to disable. `WithModifiedOnly(true)` makes the reload skip files whose mod-time and size have not changed.

A reload publishes each new template in one atomic step, so it is safe to run while other goroutines render. A render sees either the old template or the new one, never a mix.

## Benchmarks

Measured on `windows/amd64`, `AMD Ryzen 7 5700X` (8C/16T), Go 1.26.2, Rust 1.94.0.

Every cell is **time per render · renders per second on one core**. Each case
ran in its own process, six runs at a time at 150 ms each, and the fastest result
seen is shown (fastest criterion estimate seen for Rust). All engines render the same seven
templates and a test asserts their output is byte-identical. Bold marks the
fastest engine in a row.

Compared against:

- [quicktemplate](https://github.com/valyala/quicktemplate) v1.8.0 — templates compiled to Go code
- [fasttemplate](https://github.com/valyala/fasttemplate) v1.2.2 — runtime placeholder substitution
- `text/template` — Go standard library
- [sailfish](https://github.com/rust-sailfish/sailfish) 0.11.9 — Rust, templates compiled into the binary

Reproduce with:

```
cd compare
bash run.sh
cd rust && cargo bench
```

The two alostemplate columns are the same engine called two ways: **values** is
`ReplaceValues` / `AppendValues` (values in `tpl.Keys()` order, no key lookup) and
**pairs** is `Replace` / `AppendReplace` with a `[]string` of key/value pairs.

### Render to a string (cache on — the default)

Each engine's own string-returning call, given the same values every time.
alostemplate serves these from its render cache; the other engines have no cache
and render again.

| Template | alos values | alos pairs | quicktemplate | fasttemplate | text/template | sailfish (Rust) |
|---|---:|---:|---:|---:|---:|---:|
| Tiny · 21 B · 1 value | **6.82 ns · 147 M/s** | 9.30 ns · 108 M/s | 62.3 ns · 16.1 M/s | 66.8 ns · 15.0 M/s | 317 ns · 3.15 M/s | 35.3 ns · 28.3 M/s |
| Small · 250 B · 4 values | **8.87 ns · 113 M/s** | 16.3 ns · 61.5 M/s | 129 ns · 7.74 M/s | 352 ns · 2.84 M/s | 930 ns · 1.08 M/s | 42.5 ns · 23.5 M/s |
| Medium · 2.6 KB · 16 values | **31.6 ns · 31.7 M/s** | 56.9 ns · 17.6 M/s | 592 ns · 1.69 M/s | 2,031 ns · 492 K/s | 3,926 ns · 255 K/s | 123 ns · 8.12 M/s |
| Large · 35 KB · 64 values | **108 ns · 9.22 M/s** | 215 ns · 4.66 M/s | 5,277 ns · 190 K/s | 7,514 ns · 133 K/s | 31,058 ns · 32.2 K/s | 814 ns · 1.23 M/s |
| Huge · 148 KB · 8 values | **33.0 ns · 30.3 M/s** | 46.3 ns · 21.6 M/s | 13,142 ns · 76.1 K/s | 15,002 ns · 66.7 K/s | 51,219 ns · 19.5 K/s | 2,738 ns · 365 K/s |
| Long values · 33 KB · 8 × 4 KB | **477 ns · 2.10 M/s** | 496 ns · 2.02 M/s | 4,179 ns · 239 K/s | 9,240 ns · 108 K/s | 17,362 ns · 57.6 K/s | 656 ns · 1.52 M/s |
| Many slots · 6.2 KB · 256 values | **390 ns · 2.56 M/s** | 774 ns · 1.29 M/s | 4,177 ns · 239 K/s | 9,534 ns · 105 K/s | 46,681 ns · 21.4 K/s | 1,092 ns · 916 K/s |

The same cache hits spread across all 16 threads:

| Template | alos values, 16 threads | alos pairs, 16 threads |
|---|---:|---:|
| Tiny · 21 B · 1 value | 0.90 ns · 1.11 B/s | 1.32 ns · 758 M/s |
| Small · 250 B · 4 values | 1.26 ns · 794 M/s | 2.33 ns · 429 M/s |
| Medium · 2.6 KB · 16 values | 4.73 ns · 211 M/s | 8.69 ns · 115 M/s |
| Large · 35 KB · 64 values | 16.7 ns · 60.0 M/s | 32.4 ns · 30.9 M/s |
| Huge · 148 KB · 8 values | 4.75 ns · 211 M/s | 6.69 ns · 149 M/s |
| Long values · 33 KB · 8 × 4 KB | 66.8 ns · 15.0 M/s | 68.4 ns · 14.6 M/s |
| Many slots · 6.2 KB · 256 values | 32.7 ns · 30.6 M/s | 78.2 ns · 12.8 M/s |

### Render into a reused buffer (no cache, no allocations)

Every engine writes into a buffer it is handed, so nothing is allocated and
nothing is cached. This is a real render on every call.

| Template | alos values | alos pairs | quicktemplate | fasttemplate | text/template | sailfish (Rust) |
|---|---:|---:|---:|---:|---:|---:|
| Tiny · 21 B · 1 value | 8.19 ns · 122 M/s | 13.9 ns · 71.8 M/s | 33.3 ns · 30.0 M/s | 38.9 ns · 25.7 M/s | 222 ns · 4.51 M/s | **5.06 ns · 198 M/s** |
| Small · 250 B · 4 values | 14.8 ns · 67.5 M/s | 25.5 ns · 39.2 M/s | 71.2 ns · 14.0 M/s | 166 ns · 6.04 M/s | 676 ns · 1.48 M/s | **11.6 ns · 86.1 M/s** |
| Medium · 2.6 KB · 16 values | **62.9 ns · 15.9 M/s** | 92.7 ns · 10.8 M/s | 235 ns · 4.26 M/s | 654 ns · 1.53 M/s | 2,479 ns · 403 K/s | 84.6 ns · 11.8 M/s |
| Large · 35 KB · 64 values | **557 ns · 1.80 M/s** | 687 ns · 1.45 M/s | 1,106 ns · 904 K/s | 3,123 ns · 320 K/s | 10,024 ns · 99.8 K/s | 632 ns · 1.58 M/s |
| Huge · 148 KB · 8 values | **2,116 ns · 473 K/s** | 2,139 ns · 468 K/s | 2,269 ns · 441 K/s | 2,696 ns · 371 K/s | 3,850 ns · 260 K/s | 2,431 ns · 411 K/s |
| Long values · 33 KB · 8 × 4 KB | **483 ns · 2.07 M/s** | 505 ns · 1.98 M/s | 574 ns · 1.74 M/s | 5,070 ns · 197 K/s | 1,946 ns · 514 K/s | 511 ns · 1.96 M/s |
| Many slots · 6.2 KB · 256 values | **318 ns · 3.15 M/s** | 715 ns · 1.40 M/s | 3,055 ns · 327 K/s | 8,450 ns · 118 K/s | 38,721 ns · 25.8 K/s | 1,054 ns · 949 K/s |

alostemplate is the fastest engine on Medium, Large, Huge, Long values and Many slots (3.3× sailfish on Many slots). sailfish is faster on Tiny and Small: its template is compiled into the program, so it has no template to walk at run time, and at that size the fixed cost of a call decides the result.

### Render to a new string with the cache off

`alos.New(alos.WithRenderCache(alos.RenderCacheDisabled))`. Every call allocates
a fresh string.

| Template | alos values | alos pairs | quicktemplate | fasttemplate | text/template | sailfish (Rust) |
|---|---:|---:|---:|---:|---:|---:|
| Tiny · 21 B · 1 value | **28.2 ns · 35.4 M/s** | 33.4 ns · 29.9 M/s | 62.3 ns · 16.1 M/s | 66.8 ns · 15.0 M/s | 317 ns · 3.15 M/s | 35.3 ns · 28.3 M/s |
| Small · 250 B · 4 values | 60.1 ns · 16.6 M/s | 70.1 ns · 14.3 M/s | 129 ns · 7.74 M/s | 352 ns · 2.84 M/s | 930 ns · 1.08 M/s | **42.5 ns · 23.5 M/s** |
| Medium · 2.6 KB · 16 values | 422 ns · 2.37 M/s | 440 ns · 2.27 M/s | 592 ns · 1.69 M/s | 2,031 ns · 492 K/s | 3,926 ns · 255 K/s | **123 ns · 8.12 M/s** |
| Large · 35 KB · 64 values | 3,957 ns · 253 K/s | 4,030 ns · 248 K/s | 5,277 ns · 190 K/s | 7,514 ns · 133 K/s | 31,058 ns · 32.2 K/s | **814 ns · 1.23 M/s** |
| Huge · 148 KB · 8 values | 12,245 ns · 81.7 K/s | 10,960 ns · 91.2 K/s | 13,142 ns · 76.1 K/s | 15,002 ns · 66.7 K/s | 51,219 ns · 19.5 K/s | **2,738 ns · 365 K/s** |
| Long values · 33 KB · 8 × 4 KB | 3,507 ns · 285 K/s | 3,322 ns · 301 K/s | 4,179 ns · 239 K/s | 9,240 ns · 108 K/s | 17,362 ns · 57.6 K/s | **656 ns · 1.52 M/s** |
| Many slots · 6.2 KB · 256 values | **998 ns · 1.00 M/s** | 1,392 ns · 718 K/s | 4,177 ns · 239 K/s | 9,534 ns · 105 K/s | 46,681 ns · 21.4 K/s | 1,092 ns · 916 K/s |

Here the cost is Go's allocator, not the template engine: a fresh string costs about 0.1 ns per byte to allocate, which on the larger pages is longer than sailfish's whole render. alostemplate is still the fastest Go engine in every row and ahead of sailfish on Tiny and Many slots. Use the cache, `AppendValues` or `AppendReplace` when this path matters.

### Values vs pairs vs map

The same template rendered three ways. Pairs cost one key check per value on top
of values; a map costs one Go map lookup per placeholder.

Cache hits:

| Template | values | pairs | map |
|---|---:|---:|---:|
| Tiny · 21 B · 1 value | 6.82 ns · 147 M/s | 9.30 ns · 108 M/s | 19.6 ns · 51.0 M/s |
| Small · 250 B · 4 values | 8.87 ns · 113 M/s | 16.3 ns · 61.5 M/s | 48.8 ns · 20.5 M/s |
| Medium · 2.6 KB · 16 values | 31.6 ns · 31.7 M/s | 56.9 ns · 17.6 M/s | 182 ns · 5.50 M/s |
| Large · 35 KB · 64 values | 108 ns · 9.22 M/s | 215 ns · 4.66 M/s | 720 ns · 1.39 M/s |
| Huge · 148 KB · 8 values | 33.0 ns · 30.3 M/s | 46.3 ns · 21.6 M/s | 109 ns · 9.20 M/s |
| Long values · 33 KB · 8 × 4 KB | 477 ns · 2.10 M/s | 496 ns · 2.02 M/s | 562 ns · 1.78 M/s |
| Many slots · 6.2 KB · 256 values | 390 ns · 2.56 M/s | 774 ns · 1.29 M/s | 2,832 ns · 353 K/s |

Into a reused buffer:

| Template | values | pairs | map |
|---|---:|---:|---:|
| Tiny · 21 B · 1 value | 8.19 ns · 122 M/s | 13.9 ns · 71.8 M/s | 21.6 ns · 46.2 M/s |
| Small · 250 B · 4 values | 14.8 ns · 67.5 M/s | 25.5 ns · 39.2 M/s | 54.0 ns · 18.5 M/s |
| Medium · 2.6 KB · 16 values | 62.9 ns · 15.9 M/s | 92.7 ns · 10.8 M/s | 219 ns · 4.57 M/s |
| Large · 35 KB · 64 values | 557 ns · 1.80 M/s | 687 ns · 1.45 M/s | 1,248 ns · 801 K/s |
| Huge · 148 KB · 8 values | 2,116 ns · 473 K/s | 2,139 ns · 468 K/s | 2,210 ns · 452 K/s |
| Long values · 33 KB · 8 × 4 KB | 483 ns · 2.07 M/s | 505 ns · 1.98 M/s | 564 ns · 1.77 M/s |
| Many slots · 6.2 KB · 256 values | 318 ns · 3.15 M/s | 715 ns · 1.40 M/s | 2,980 ns · 336 K/s |

### Cache hits do not depend on page size

A hit hashes the values and reads one cache slot, so the page can be any size.
One placeholder, cache enabled:

| Page size | Cache hit | Full render (cache off) |
|---|---:|---:|
| 1 KB | 6.67 ns · 150 M/s | 174 ns · 5.73 M/s |
| 8 KB | 6.62 ns · 151 M/s | 818 ns · 1.22 M/s |
| 32 KB | 6.56 ns · 152 M/s | 3,514 ns · 285 K/s |
| 128 KB | 6.67 ns · 150 M/s | 8,344 ns · 120 K/s |

### Hit cost tracks how many values you pass

Small page, varying placeholder count, cache hits:

| Values | `[]string` pairs | `map[string]string` |
|---|---:|---:|
| 0 | 2.24 ns · 446 M/s | 2.19 ns · 457 M/s |
| 1 | 6.82 ns · 147 M/s | 17.6 ns · 56.9 M/s |
| 4 | 13.8 ns · 72.6 M/s | 46.5 ns · 21.5 M/s |
| 8 | 24.2 ns · 41.4 M/s | 88.7 ns · 11.3 M/s |
| 16 | 47.2 ns · 21.2 M/s | 174 ns · 5.75 M/s |
| 64 | 196 ns · 5.09 M/s | 695 ns · 1.44 M/s |

A template with no placeholders is returned as-is without copying. All of the
above are **0 allocations**.

### Loading

| Operation | Time |
|---|---:|
| `Load` — cached single file | `93 µs` |
| `Load` — cached directory bundle | `272 µs` |
| `Named` lookup on a bundle | `13.7 ns` |

`Load` re-stats the file on every call to detect on-disk changes, and that syscall is most of its cost on Windows. Call `Load` once at startup and hold the returned `*Template`; do not call it per request.

### Notes on reading these numbers

- Renders per second is `1 / time` on one core. It is not a web-server throughput figure.
- Cache hits, `AppendValues` and `AppendReplace` allocate nothing. A render that is not served from the cache allocates the page, plus one small cache entry when the page is stored.
- Results that allocate move by 20% or more from run to run because of GC timing, and by 2–3× depending on what else the process has allocated. Zero-allocation rows are stable to within about 10%.

## Examples

### 1. Load and Render a Single File

`greeting.alos`:
```html
<h1>Welcome, {{name}}!</h1>
<p>Your role is: {{role}}</p>
```

```go
package main

import (
    "fmt"
    alos "github.com/guno1928/alostemplate"
)

func main() {
    tpl, err := alos.Load("greeting.alos")
    if err != nil {
        panic(err)
    }

    out, err := alos.Replace(tpl, map[string]string{
        "name": "Alice",
        "role": "Admin",
    })
    if err != nil {
        panic(err)
    }

    fmt.Println(out)
    // <h1>Welcome, Alice!</h1>
    // <p>Your role is: Admin</p>
}
```

### 2. Load a Bundle with Explicit Includes

`templates/index.alos`:
```html
<body>
{{include "nav"}}
<main>
    <h1>{{title}}</h1>
    <p>{{subtitle}}</p>
</main>
{{include "footer"}}
</body>
```

`templates/nav.alos`:
```html
<nav class="{{active}}">
    <a href="/">Home</a>
    <a href="/docs">Docs</a>
    <a href="/api">API</a>
</nav>
```

`templates/footer.alos`:
```html
<footer>
    <p>&copy; {{copyright}} ALOS CDN</p>
</footer>
```

```go
package main

import (
    "fmt"
    alos "github.com/guno1928/alostemplate"
)

func main() {
    bundle, err := alos.Load("templates")
    if err != nil {
        panic(err)
    }

    out, err := alos.Replace(bundle, map[string]string{
        "title":     "Documentation",
        "subtitle":  "Fast placeholder replacement for Go web servers",
        "active":    "docs-selected",
        "copyright": "2026",
    })
    if err != nil {
        panic(err)
    }

    fmt.Println(out)
    // The nav and footer are inlined into index.alos at load time via include.
    // All four placeholders are replaced at render time.
}
```

### 3. Custom Delimiters

```go
package main

import (
    "fmt"
    "os"
    alos "github.com/guno1928/alostemplate"
)

func main() {
    engine := alos.New(alos.WithDelimiters("<%", "%>"))
    defer engine.Stop()

    os.WriteFile("page.alos", []byte(
        "<html><head><title><%title%></title></head>"+
            "<body><h1><%heading%></h1><p><%content%></p></body></html>",
    ), 0644)

    tpl, err := engine.Load("page.alos")
    if err != nil {
        panic(err)
    }

    out, err := alos.Replace(tpl, map[string]string{
        "title":   "My Page",
        "heading": "Welcome",
        "content": "This uses <% %> delimiters.",
    })
    if err != nil {
        panic(err)
    }

    fmt.Println(out)
}
```

### 4. Auto-Refresh Every 30 Seconds

```go
package main

import (
    "fmt"
    "net/http"
    "time"
    alos "github.com/guno1928/alostemplate"
)

func main() {
    engine := alos.New(
        alos.WithAutoRefresh(30 * time.Second),
        alos.WithModifiedOnly(true),
    )
    defer engine.Stop()

    bundle, err := engine.Load("templates")
    if err != nil {
        panic(err)
    }
    index := bundle.Named("index")

    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        out, err := alos.Replace(index, map[string]string{
            "title":     "Live Page",
            "subtitle":  "Edit the .alos files and they reload within 30s",
            "active":    "home",
            "copyright": "2026",
        })
        if err != nil {
            http.Error(w, err.Error(), 500)
            return
        }
        w.Header().Set("Content-Type", "text/html")
        io.WriteString(w, out)
    })

    fmt.Println("Listening on :8080 with 30s auto-refresh")
    http.ListenAndServe(":8080", nil)
}
```

### 5. Change Auto-Refresh at Runtime

```go
package main

import (
    "fmt"
    "time"
    alos "github.com/guno1928/alostemplate"
)

func main() {
    engine := alos.New(alos.WithAutoRefresh(time.Minute))
    defer engine.Stop()

    fmt.Println("Auto-refresh:", engine.AutoRefresh()) // 1m0s

    // Speed up during development
    engine.SetAutoRefresh(5 * time.Second)
    fmt.Println("Auto-refresh:", engine.AutoRefresh()) // 5s

    // Disable completely
    engine.SetAutoRefresh(0)
    fmt.Println("Auto-refresh:", engine.AutoRefresh()) // 0s
}
```

### 6. Two Engines With Different Delimiters

```go
package main

import (
    "fmt"
    "os"
    alos "github.com/guno1928/alostemplate"
)

func main() {
    os.WriteFile("curly.alos", []byte("<h1>{{title}}</h1>"), 0644)
    os.WriteFile("bracket.alos", []byte("<h1>[%title%]</h1>"), 0644)

    curly := alos.New()
    defer curly.Stop()

    bracket := alos.New(alos.WithDelimiters("[%", "%]"))
    defer bracket.Stop()

    tpl1, _ := curly.Load("curly.alos")
    tpl2, _ := bracket.Load("bracket.alos")

    out1, _ := alos.Replace(tpl1, map[string]string{"title": "Curly"})
    out2, _ := alos.Replace(tpl2, map[string]string{"title": "Bracket"})

    fmt.Println(string(out1)) // <h1>Curly</h1>
    fmt.Println(string(out2)) // <h1>Bracket</h1>
}
```

### 7. Render a Partial from a Bundle

```go
package main

import (
    "fmt"
    alos "github.com/guno1928/alostemplate"
)

func main() {
    bundle, err := alos.Load("templates")
    if err != nil {
        panic(err)
    }

    nav := bundle.Named("nav")
    if nav == nil {
        panic("missing nav template")
    }

    out, err := alos.Replace(nav, map[string]string{
        "active": "home-selected",
    })
    if err != nil {
        panic(err)
    }

    fmt.Println(out)
}
```

### 8. List All Templates in a Bundle

```go
package main

import (
    "fmt"
    alos "github.com/guno1928/alostemplate"
)

func main() {
    bundle, err := alos.Load("templates")
    if err != nil {
        panic(err)
    }

    fmt.Println("Templates in bundle:")
    for _, name := range bundle.Names() {
        tpl := bundle.Named(name)
        fmt.Printf("  %-20s  file: %s\n", name, tpl.FileName())
    }
}
```

### 9. Replace With String Shorthand

`title.alos`:
```html
<h1>{{changeme}}</h1>
```

```go
package main

import (
    "fmt"
    alos "github.com/guno1928/alostemplate"
)

func main() {
    tpl, err := alos.Load("title.alos")
    if err != nil {
        panic(err)
    }

    // When a template has one placeholder, just pass a string.
    // The string fills that single placeholder regardless of its key name.
    out, err := alos.Replace(tpl, "Documentation Hub")
    if err != nil {
        panic(err)
    }

    fmt.Println(out)
    // <h1>Documentation Hub</h1>
}
```

### 10. Replace With Flat Key/Value Pairs

```go
package main

import (
    "fmt"
    alos "github.com/guno1928/alostemplate"
)

func main() {
    tpl, err := alos.Load("page.alos")
    if err != nil {
        panic(err)
    }

    out, err := alos.Replace(tpl, []string{
        "title", "Docs",
        "subtitle", "Fast placeholder replacement",
    })
    if err != nil {
        panic(err)
    }

    fmt.Println(out)
}
```

### 11. Force Reload From Disk

```go
package main

import (
    "fmt"
    "os"
    alos "github.com/guno1928/alostemplate"
)

func main() {
    os.WriteFile("live.alos", []byte("<p>{{msg}}</p>"), 0644)

    tpl, err := alos.Load("live.alos")
    if err != nil {
        panic(err)
    }

    out, _ := alos.Replace(tpl, map[string]string{"msg": "V1"})
    fmt.Println(out) // <p>V1</p>

    // Edit the file on disk
    os.WriteFile("live.alos", []byte("<div>{{msg}}</div>"), 0644)

    // Force reload — the same template handle reflects the new content
    if err := alos.Reload(); err != nil {
        panic(err)
    }

    out, _ = alos.Replace(tpl, map[string]string{"msg": "V2"})
    fmt.Println(out) // <div>V2</div>
}
```

### 12. Template-Level Reload

```go
package main

import (
    "fmt"
    "os"
    alos "github.com/guno1928/alostemplate"
)

func main() {
    os.WriteFile("status.alos", []byte("Status: {{state}}"), 0644)

    engine := alos.New()
    defer engine.Stop()

    tpl, _ := engine.Load("status.alos")
    out, _ := alos.Replace(tpl, map[string]string{"state": "OK"})
    fmt.Println(out) // Status: OK

    os.WriteFile("status.alos", []byte("Current: {{state}}"), 0644)
    tpl.Reload()

    out, _ = alos.Replace(tpl, map[string]string{"state": "Updated"})
    fmt.Println(out) // Current: Updated
}
```

### 13. Include With Custom Delimiters

`views/layout.alos`:
```html
<html>
<head><title><%title%></title></head>
<body>
<%include "header"%>
<main><%content%></main>
<%include "footer"%>
</body>
</html>
```

`views/header.alos`:
```html
<header>
    <h1><%sitename%></h1>
    <nav><%navlinks%></nav>
</header>
```

`views/footer.alos`:
```html
<footer>&copy; <%year%> <%sitename%></footer>
```

```go
package main

import (
    "fmt"
    alos "github.com/guno1928/alostemplate"
)

func main() {
    engine := alos.New(alos.WithDelimiters("<%", "%>"))
    defer engine.Stop()

    bundle, err := engine.Load("views")
    if err != nil {
        panic(err)
    }

    layout := bundle.Named("layout")
    out, err := alos.Replace(layout, map[string]string{
        "title":    "Home",
        "sitename": "ALOS CDN",
        "navlinks": "<a>Home</a> | <a>Docs</a>",
        "content":  "<p>Welcome to the site.</p>",
        "year":     "2026",
    })
    if err != nil {
        panic(err)
    }

    fmt.Println(out)
}
```

### 14. Chained Includes

`parts/index.alos`:
```html
<body>{{include "wrapper"}}</body>
```

`parts/wrapper.alos`:
```html
<div class="wrapper">{{include "content"}}</div>
```

`parts/content.alos`:
```html
<article><h1>{{title}}</h1><p>{{body}}</p></article>
```

```go
package main

import (
    "fmt"
    alos "github.com/guno1928/alostemplate"
)

func main() {
    engine := alos.New()
    defer engine.Stop()

    bundle, _ := engine.Load("parts")
    out, _ := alos.Replace(bundle, map[string]string{
        "title": "Deep Include",
        "body":  "This was included through two levels.",
    })

    fmt.Println(out)
    // <body><div class="wrapper"><article><h1>Deep Include</h1>
    // <p>This was included through two levels.</p></article></div></body>
}
```

### 15. Bare Placeholders Are NOT Auto-Included

```go
package main

import (
    "fmt"
    "os"
    alos "github.com/guno1928/alostemplate"
)

func main() {
    os.MkdirAll("demo", 0755)
    os.WriteFile("demo/index.alos",
        []byte("<body>{{nav}}<main>{{title}}</main></body>"), 0644)
    os.WriteFile("demo/nav.alos",
        []byte("<nav>Links</nav>"), 0644)

    engine := alos.New()
    defer engine.Stop()

    bundle, _ := engine.Load("demo")
    out, _ := alos.Replace(bundle, map[string]string{
        "title": "Page Title",
    })

    fmt.Println(out)
    // <body>{{nav}}<main>Page Title</main></body>
    //
    // {{nav}} stays as a placeholder — it is NOT replaced with nav.alos content.
    // To inline nav.alos, write {{include "nav"}} instead.
}
```

### 16. Web Server With Named Templates

> **Warning:** this example combines `WithAutoRefresh` with concurrent HTTP handlers, which currently races. See [Concurrency](#concurrency). For production, drop the `WithAutoRefresh` option and reload explicitly instead.

```go
package main

import (
    "fmt"
    "net/http"
    "time"
    alos "github.com/guno1928/alostemplate"
)

func main() {
    engine := alos.New(
        alos.WithAutoRefresh(10 * time.Second),
        alos.WithModifiedOnly(true),
    )
    defer engine.Stop()

    bundle, err := engine.Load("views")
    if err != nil {
        panic(err)
    }

    indexTpl := bundle.Named("index")
    errorTpl := bundle.Named("error")

    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        out, err := alos.Replace(indexTpl, map[string]string{
            "title":   "Home",
            "content": "<p>Welcome!</p>",
        })
        if err != nil {
            http.Error(w, err.Error(), 500)
            return
        }
        w.Header().Set("Content-Type", "text/html")
        io.WriteString(w, out)
    })

    http.HandleFunc("/error", func(w http.ResponseWriter, r *http.Request) {
        out, _ := alos.Replace(errorTpl, map[string]string{
            "title":   "Error",
            "message": "Something went wrong.",
        })
        w.Header().Set("Content-Type", "text/html")
        w.WriteHeader(500)
        io.WriteString(w, out)
    })

    fmt.Println("Listening on :8080")
    http.ListenAndServe(":8080", nil)
}
```

### 17. Missing Placeholders Stay Unchanged

```go
package main

import (
    "fmt"
    "os"
    alos "github.com/guno1928/alostemplate"
)

func main() {
    os.WriteFile("page.alos",
        []byte("<h1>{{title}}</h1><p>{{subtitle}}</p><span>{{extra}}</span>"), 0644)

    tpl, _ := alos.Load("page.alos")

    // Only provide "title" — the other two placeholders are not in the map
    out, _ := alos.Replace(tpl, map[string]string{
        "title": "Docs",
    })

    fmt.Println(out)
    // <h1>Docs</h1><p>{{subtitle}}</p><span>{{extra}}</span>
    //
    // Missing keys stay as their original placeholder text.
}
```

### 18. Single-Character Delimiters

```go
package main

import (
    "fmt"
    "os"
    alos "github.com/guno1928/alostemplate"
)

func main() {
    os.WriteFile("minimal.alos", []byte("<h1>{title}</h1><p>{body}</p>"), 0644)

    engine := alos.New(alos.WithDelimiters("{", "}"))
    defer engine.Stop()

    tpl, _ := engine.Load("minimal.alos")
    out, _ := alos.Replace(tpl, map[string]string{
        "title": "Minimal",
        "body":  "Single-char delimiters work too.",
    })

    fmt.Println(out)
    // <h1>Minimal</h1><p>Single-char delimiters work too.</p>
}
```

### 19. Change Default Engine Delimiters

```go
package main

import (
    "fmt"
    "os"
    alos "github.com/guno1928/alostemplate"
)

func main() {
    alos.SetDelimiters("<<", ">>")

    l, r := alos.Delimiters()
    fmt.Println("Delimiters:", l, r) // << >>

    os.WriteFile("arrow.alos", []byte("<h1><<title>></h1>"), 0644)

    tpl, _ := alos.Load("arrow.alos")
    out, _ := alos.Replace(tpl, map[string]string{"title": "Arrow Style"})

    fmt.Println(out)
    // <h1>Arrow Style</h1>
}
```

### 20. Multi-Route Server With Shared Bundle

> **Warning:** this example combines `WithAutoRefresh` with concurrent HTTP handlers, which currently races. See [Concurrency](#concurrency). For production, drop the `WithAutoRefresh` option and reload explicitly instead.

`site/index.alos`:
```html
<html>
<head><title>{{pagetitle}}</title></head>
<body>
{{include "nav"}}
<main>
    <h1>{{heading}}</h1>
    <div class="content">{{maincontent}}</div>
</main>
{{include "sidebar"}}
{{include "footer"}}
</body>
</html>
```

`site/nav.alos`:
```html
<nav>
    <a href="/" class="{{nav_home}}">Home</a>
    <a href="/about" class="{{nav_about}}">About</a>
    <a href="/contact" class="{{nav_contact}}">Contact</a>
</nav>
```

`site/sidebar.alos`:
```html
<aside>
    <h3>{{sidebar_title}}</h3>
    <ul>
        <li>{{sidebar_item1}}</li>
        <li>{{sidebar_item2}}</li>
        <li>{{sidebar_item3}}</li>
    </ul>
</aside>
```

`site/footer.alos`:
```html
<footer>&copy; {{year}} {{company}} | {{footer_links}}</footer>
```

```go
package main

import (
    "fmt"
    "net/http"
    "time"
    alos "github.com/guno1928/alostemplate"
)

func main() {
    engine := alos.New(
        alos.WithAutoRefresh(30 * time.Second),
        alos.WithModifiedOnly(true),
    )
    defer engine.Stop()

    bundle, err := engine.Load("site")
    if err != nil {
        panic(err)
    }

    index := bundle.Named("index")

    baseVals := map[string]string{
        "year":          "2026",
        "company":       "ALOS CDN",
        "footer_links":  "<a href='/privacy'>Privacy</a> | <a href='/terms'>Terms</a>",
        "sidebar_title": "Quick Links",
        "sidebar_item1": "Getting Started",
        "sidebar_item2": "API Reference",
        "sidebar_item3": "Examples",
    }

    merge := func(base, extra map[string]string) map[string]string {
        out := make(map[string]string, len(base)+len(extra))
        for k, v := range base {
            out[k] = v
        }
        for k, v := range extra {
            out[k] = v
        }
        return out
    }

    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        vals := merge(baseVals, map[string]string{
            "pagetitle":   "Home - ALOS",
            "heading":     "Welcome Home",
            "maincontent": "<p>This is the home page.</p>",
            "nav_home":    "active",
            "nav_about":   "",
            "nav_contact": "",
        })
        out, _ := alos.Replace(index, vals)
        w.Header().Set("Content-Type", "text/html")
        io.WriteString(w, out)
    })

    http.HandleFunc("/about", func(w http.ResponseWriter, r *http.Request) {
        vals := merge(baseVals, map[string]string{
            "pagetitle":   "About - ALOS",
            "heading":     "About Us",
            "maincontent": "<p>We build fast template engines for Go.</p>",
            "nav_home":    "",
            "nav_about":   "active",
            "nav_contact": "",
        })
        out, _ := alos.Replace(index, vals)
        w.Header().Set("Content-Type", "text/html")
        io.WriteString(w, out)
    })

    http.HandleFunc("/contact", func(w http.ResponseWriter, r *http.Request) {
        vals := merge(baseVals, map[string]string{
            "pagetitle":   "Contact - ALOS",
            "heading":     "Get In Touch",
            "maincontent": "<p>Email us at hello@alos.dev</p>",
            "nav_home":    "",
            "nav_about":   "",
            "nav_contact": "active",
        })
        out, _ := alos.Replace(index, vals)
        w.Header().Set("Content-Type", "text/html")
        io.WriteString(w, out)
    })

    fmt.Println("Listening on :8080 with auto-refresh every 30s")
    http.ListenAndServe(":8080", nil)
}
```

## Tests

179 test functions across `core/` and the root package, covering every exported and unexported function:

- Single file loading and rendering
- Directory bundle loading with explicit includes
- Custom delimiter configuration
- Include directive parsing (double quotes, single quotes, chained, cyclic, missing)
- Replace edge cases (nil template, empty values, unicode, duplicate keys, missing keys, large values, output buffer reuse)
- Auto-refresh and manual reload
- Engine lifecycle, caching, and error paths
- Package-level convenience API
- Assembly render kernel checked against the pure-Go kernel across every length class, with guard bytes around the output to catch any write outside it
- Concurrent render and concurrent load under the race detector

```
go test ./...
go test ./... -race
go vet ./...
```

Benchmark alternatives used to select the current implementation live in `core/alt_resolve*_test.go` and `core/alt_copy_test.go`. Each alternative is proven equivalent to the original before it is benchmarked, so the selection is reproducible.

## Concurrency

`Replace`, `ReplaceValues`, `AppendReplace` and `AppendValues` are safe to call concurrently on the same `*Template`. `Load` is safe to call concurrently on the same `*Engine`.

`Reload`, `Template.Reload` and the auto-refresh goroutine are also safe to run while other goroutines render. Each reload compiles a new template and publishes it through an atomic pointer, and reloads on one `Engine` run one at a time. This is covered under the race detector by `TestReloadWhileRenderingIsRaceFreeAndNeverMixesVersions` and `TestAutoRefreshConcurrentRenderIsRaceFree` in `core/`.
