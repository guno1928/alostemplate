package core

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const reloadRaceDuration = 150 * time.Millisecond

func TestReloadWhileRenderingIsRaceFreeAndNeverMixesVersions(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.alos", "I0-{{v}}")
	writeFile(t, dir, "nav.alos", "N0-{{v}}")
	e := newTestEngine(t)
	bundle, err := e.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	nav := bundle.Named("nav")
	if nav == nil {
		t.Fatal("nav missing")
	}

	var version atomic.Int64
	validIndex := func(got string) bool {
		latest := version.Load()
		for v := int64(0); v <= latest+1; v++ {
			if got == "I"+strconv.FormatInt(v, 10)+"-x" {
				return true
			}
		}
		return false
	}
	validNav := func(got string) bool {
		latest := version.Load()
		for v := int64(0); v <= latest+1; v++ {
			if got == "N"+strconv.FormatInt(v, 10)+"-x" {
				return true
			}
		}
		return false
	}

	stop := make(chan struct{})
	var bad atomic.Value
	var readers sync.WaitGroup
	for range 8 {
		readers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				if got := Replace(bundle, []string{"v", "x"}); !validIndex(got) {
					bad.Store("bundle render " + got)
				}
				if got := Replace(nav, []string{"v", "x"}); !validNav(got) {
					bad.Store("nav handle render " + got)
				}
				if child := bundle.Named("nav"); child == nil {
					bad.Store("Named returned nil during reload")
				} else if got := ReplaceMap(child, map[string]string{"v": "x"}); !validNav(got) {
					bad.Store("Named render " + got)
				}
				if names := bundle.Names(); len(names) != 2 {
					bad.Store("Names returned " + strconv.Itoa(len(names)))
				}
				if bundle.Name() != "index" || nav.FileName() != "nav.alos" {
					bad.Store("Name/FileName changed")
				}
				bundle.ClearRenderCache()
			}
		})
	}

	deadline := time.Now().Add(reloadRaceDuration)
	for v := int64(1); time.Now().Before(deadline); v++ {
		s := strconv.FormatInt(v, 10)
		version.Store(v - 1)
		writeFile(t, dir, "index.alos", "I"+s+"-{{v}}")
		writeFile(t, dir, "nav.alos", "N"+s+"-{{v}}")
		version.Store(v)
		if err := e.Reload(); err != nil {
			t.Fatalf("Reload: %v", err)
		}
		if v%3 == 0 {
			if err := nav.Reload(); err != nil {
				t.Fatalf("nav.Reload: %v", err)
			}
		}
	}
	close(stop)
	readers.Wait()
	if msg := bad.Load(); msg != nil {
		t.Fatalf("inconsistent read during reload: %v", msg)
	}

	final := strconv.FormatInt(version.Load(), 10)
	if got := Replace(bundle, []string{"v", "x"}); got != "I"+final+"-x" {
		t.Fatalf("after reloads bundle = %q want I%s-x", got, final)
	}
	if got := Replace(nav, []string{"v", "x"}); got != "N"+final+"-x" {
		t.Fatalf("after reloads nav = %q want N%s-x", got, final)
	}
}

func TestRenderCacheNeverServesContentFromBeforeAReload(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "page.alos", "OLD {{v}}")
	e := newTestEngine(t, WithRenderCache(time.Hour))
	tpl, err := e.Load(dir + "/page.alos")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := Replace(tpl, []string{"v", "x"}); got != "OLD x" {
		t.Fatalf("first render = %q", got)
	}
	writeFile(t, dir, "page.alos", "NEW {{v}}")
	if err := tpl.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := Replace(tpl, []string{"v", "x"}); got != "NEW x" {
		t.Fatalf("cached render after reload = %q want NEW x", got)
	}
}

func TestAutoRefreshIntervalReadIsRaceFree(t *testing.T) {
	e := newTestEngine(t)
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range 200 {
			e.SetAutoRefresh(time.Duration(i%2) * time.Hour)
		}
	})
	for range 200 {
		_ = e.AutoRefresh()
	}
	wg.Wait()
}
