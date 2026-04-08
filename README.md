# alostemplate

A fast, configurable `.alos` template engine for Go. Load template files once, replace placeholders at high speed, and optionally auto-refresh from disk on a timer.

Features:

- **Configurable delimiters** — use `{{` `}}`, `<%` `%>`, `[%` `%]`, or anything you want
- **Explicit includes** — `{{include "nav"}}` pulls one file into another at load time
- **Auto-refresh** — set an interval and templates reload from disk automatically
- **1 alloc/op** rendering with precompiled slot-based templates
- **[alosmap](https://github.com/guno1928/alosmap)** for high-performance concurrent file caching

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
out, err := alos.Replace(tpl, nil, map[string]string{
    "title": "Hello World",
})
if err != nil {
    panic(err)
}
fmt.Println(string(out))
```

## Public API

### Package-Level (Default Engine)

| Function | Description |
|---|---|
| `Load(path)` | Load a single `.alos` file or a directory bundle |
| `Reload()` | Force re-read all loaded templates from disk |
| `Replace(tpl, dst, values)` | Render a template with placeholder values |
| `SetDelimiters(left, right)` | Change delimiters for future loads |
| `SetAutoRefresh(interval)` | Enable/disable periodic auto-reload (0 = off) |
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
| `engine.Delimiters()` | Get current delimiters |
| `engine.AutoRefresh()` | Get current auto-refresh interval |
| `engine.Stop()` | Stop engine and release resources |

### Options

| Option | Default | Description |
|---|---|---|
| `WithDelimiters(l, r)` | `{{`, `}}` | Set placeholder delimiters |
| `WithAutoRefresh(d)` | `0` (off) | Auto-reload interval |
| `WithModifiedOnly(bool)` | `false` | Only reload files whose on-disk signature changed |

### Template Methods

| Method | Description |
|---|---|
| `tpl.Named(name)` | Get a child template from a bundle by name |
| `tpl.Names()` | List all template names in a bundle |
| `tpl.Name()` | Template's logical name (path without `.alos` extension) |
| `tpl.FileName()` | Template's source file name |
| `tpl.Reload()` | Reload this specific template from disk |

### Replace Inputs

`Replace(tpl, dst, values)` accepts three value types:

- **`string`** — single-value shorthand (fills the first placeholder)
- **`[]string`** — flat `"key","value","key","value"` pairs
- **`map[string]string`** — key→value map (recommended)

Pass `nil` for `dst` unless you manage your own output buffer.

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

`Replace` takes a compiled template and a set of values. It walks the precompiled slot list, looks up each placeholder key in the provided values, and writes the result into a single `[]byte` allocation.

- If a placeholder key is found in the values, the value is substituted.
- If a placeholder key is **not** found, the original placeholder text (e.g. `{{title}}`) is left in the output unchanged. This makes missing keys visible during development.

For templates with exactly one placeholder, a fast path using `unsafe.Pointer` + `memmove` is used. For templates with ≤4 placeholders, a stack-allocated array avoids pool overhead.

## Reload

`Reload()` walks everything already loaded by the engine, re-reads files from disk, and updates the compiled templates in place. Existing template handles (pointers returned by `Load` or `Named`) remain valid after reload — they are updated, not replaced. This means you can hold onto a `*Template` at server startup and it will reflect the latest file content after any reload.

`tpl.Reload()` reloads a single template (or bundle) rather than all loaded templates.

`SetAutoRefresh(interval)` starts a background goroutine that calls `Reload()` on every tick. Pass 0 to disable. `WithModifiedOnly(true)` makes the reload skip files whose mod-time and size have not changed.

## Benchmarks

Benchmarks run on `windows/amd64` with an `AMD Ryzen 7 5700X`.

Template bundle: `index.alos` (includes nav + footer via `{{include "..."}}`), `nav.alos`, `footer.alos`.
Values: `title`, `subtitle`, `active`, `copyright`.
Measured: repeated `Replace(bundle, nil, map[string]string{...})` on an already-loaded bundle.

```
go test -run ^$ -bench "^BenchmarkReplace$" -benchmem -count=1
```

| Benchmark | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `small_1_panel_3_items` | `156.1` | `352` | `1` |
| `medium_4_panels_12_items` | `292.0` | `896` | `1` |
| `large_12_panels_36_items` | `710.8` | `2688` | `1` |
| `xlarge_32_panels_96_items` | `1335` | `6144` | `1` |

1 alloc/op across all sizes. The single allocation is the returned `[]byte`.

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

    out, err := alos.Replace(tpl, nil, map[string]string{
        "name": "Alice",
        "role": "Admin",
    })
    if err != nil {
        panic(err)
    }

    fmt.Println(string(out))
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

    out, err := alos.Replace(bundle, nil, map[string]string{
        "title":     "Documentation",
        "subtitle":  "Fast placeholder replacement for Go web servers",
        "active":    "docs-selected",
        "copyright": "2026",
    })
    if err != nil {
        panic(err)
    }

    fmt.Println(string(out))
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

    out, err := alos.Replace(tpl, nil, map[string]string{
        "title":   "My Page",
        "heading": "Welcome",
        "content": "This uses <% %> delimiters.",
    })
    if err != nil {
        panic(err)
    }

    fmt.Println(string(out))
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
        out, err := alos.Replace(index, nil, map[string]string{
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
        w.Write(out)
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

    out1, _ := alos.Replace(tpl1, nil, map[string]string{"title": "Curly"})
    out2, _ := alos.Replace(tpl2, nil, map[string]string{"title": "Bracket"})

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

    out, err := alos.Replace(nav, nil, map[string]string{
        "active": "home-selected",
    })
    if err != nil {
        panic(err)
    }

    fmt.Println(string(out))
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
    out, err := alos.Replace(tpl, nil, "Documentation Hub")
    if err != nil {
        panic(err)
    }

    fmt.Println(string(out))
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

    out, err := alos.Replace(tpl, nil, []string{
        "title", "Docs",
        "subtitle", "Fast placeholder replacement",
    })
    if err != nil {
        panic(err)
    }

    fmt.Println(string(out))
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

    out, _ := alos.Replace(tpl, nil, map[string]string{"msg": "V1"})
    fmt.Println(string(out)) // <p>V1</p>

    // Edit the file on disk
    os.WriteFile("live.alos", []byte("<div>{{msg}}</div>"), 0644)

    // Force reload — the same template handle reflects the new content
    if err := alos.Reload(); err != nil {
        panic(err)
    }

    out, _ = alos.Replace(tpl, nil, map[string]string{"msg": "V2"})
    fmt.Println(string(out)) // <div>V2</div>
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
    out, _ := alos.Replace(tpl, nil, map[string]string{"state": "OK"})
    fmt.Println(string(out)) // Status: OK

    os.WriteFile("status.alos", []byte("Current: {{state}}"), 0644)
    tpl.Reload()

    out, _ = alos.Replace(tpl, nil, map[string]string{"state": "Updated"})
    fmt.Println(string(out)) // Current: Updated
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
    out, err := alos.Replace(layout, nil, map[string]string{
        "title":    "Home",
        "sitename": "ALOS CDN",
        "navlinks": "<a>Home</a> | <a>Docs</a>",
        "content":  "<p>Welcome to the site.</p>",
        "year":     "2026",
    })
    if err != nil {
        panic(err)
    }

    fmt.Println(string(out))
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
    out, _ := alos.Replace(bundle, nil, map[string]string{
        "title": "Deep Include",
        "body":  "This was included through two levels.",
    })

    fmt.Println(string(out))
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
    out, _ := alos.Replace(bundle, nil, map[string]string{
        "title": "Page Title",
    })

    fmt.Println(string(out))
    // <body>{{nav}}<main>Page Title</main></body>
    //
    // {{nav}} stays as a placeholder — it is NOT replaced with nav.alos content.
    // To inline nav.alos, write {{include "nav"}} instead.
}
```

### 16. Web Server With Named Templates

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
        out, err := alos.Replace(indexTpl, nil, map[string]string{
            "title":   "Home",
            "content": "<p>Welcome!</p>",
        })
        if err != nil {
            http.Error(w, err.Error(), 500)
            return
        }
        w.Header().Set("Content-Type", "text/html")
        w.Write(out)
    })

    http.HandleFunc("/error", func(w http.ResponseWriter, r *http.Request) {
        out, _ := alos.Replace(errorTpl, nil, map[string]string{
            "title":   "Error",
            "message": "Something went wrong.",
        })
        w.Header().Set("Content-Type", "text/html")
        w.WriteHeader(500)
        w.Write(out)
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
    out, _ := alos.Replace(tpl, nil, map[string]string{
        "title": "Docs",
    })

    fmt.Println(string(out))
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
    out, _ := alos.Replace(tpl, nil, map[string]string{
        "title": "Minimal",
        "body":  "Single-char delimiters work too.",
    })

    fmt.Println(string(out))
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
    out, _ := alos.Replace(tpl, nil, map[string]string{"title": "Arrow Style"})

    fmt.Println(string(out))
    // <h1>Arrow Style</h1>
}
```

### 20. Multi-Route Server With Shared Bundle

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
        out, _ := alos.Replace(index, nil, vals)
        w.Header().Set("Content-Type", "text/html")
        w.Write(out)
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
        out, _ := alos.Replace(index, nil, vals)
        w.Header().Set("Content-Type", "text/html")
        w.Write(out)
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
        out, _ := alos.Replace(index, nil, vals)
        w.Header().Set("Content-Type", "text/html")
        w.Write(out)
    })

    fmt.Println("Listening on :8080 with auto-refresh every 30s")
    http.ListenAndServe(":8080", nil)
}
```

## Tests

The `testallnow/` directory contains 100 tests covering:

- Single file loading and rendering
- Directory bundle loading with explicit includes
- Custom delimiter configuration
- Include directive parsing (double quotes, single quotes, chained, missing)
- Replace edge cases (nil template, empty values, unicode, large values)
- Auto-refresh and manual reload
- Engine lifecycle, caching, and error paths
- Package-level convenience API

```
