package core

import (
	"math"
	"sync"
	"testing"
	"time"
)

func loadTemplateFile(t *testing.T, e *Engine, path string) *Template {
	t.Helper()
	tpl, err := e.Load(path)
	if err != nil {
		t.Fatalf("Load(%s): %v", path, err)
	}
	return tpl
}

type namedHandle struct {
	name   string
	handle *Template
}

func editedTwiceHandles(t *testing.T, e *Engine) (string, []namedHandle) {
	t.Helper()
	dir := t.TempDir()
	path := writeFile(t, dir, "page.alos", "one {{a}}")
	beforeEdit := loadTemplateFile(t, e, path)
	writeFile(t, dir, "page.alos", "second {{a}}")
	afterEdit := loadTemplateFile(t, e, path)
	writeFile(t, dir, "page.alos", "the third {{a}}")
	return "the third 1", []namedHandle{
		{"handle loaded after the first edit", afterEdit},
		{"handle loaded before the first edit", beforeEdit},
	}
}

func TestHandleLoadedBeforeAnEditFollowsALaterEngineReload(t *testing.T) {
	e := newTestEngine(t)
	want, handles := editedTwiceHandles(t, e)
	if err := e.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	for _, h := range handles {
		if got := Replace(h.handle, []string{"a", "1"}); got != want {
			t.Errorf("%s rendered %q after Engine.Reload, want %q", h.name, got, want)
		}
	}
}

func TestHandleLoadedBeforeAnEditFollowsItsOwnReload(t *testing.T) {
	e := newTestEngine(t)
	want, handles := editedTwiceHandles(t, e)
	for _, h := range handles {
		if err := h.handle.Reload(); err != nil {
			t.Fatalf("%s Reload: %v", h.name, err)
		}
		if got := Replace(h.handle, []string{"a", "1"}); got != want {
			t.Errorf("%s rendered %q after its own Reload, want %q", h.name, got, want)
		}
	}
}

func TestLoadOfAnEditedFileUpdatesTheHandleAlreadyHandedOut(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "page.alos", "one {{a}}")
	e := newTestEngine(t)
	beforeEdit := loadTemplateFile(t, e, path)
	writeFile(t, dir, "page.alos", "second {{a}}")
	afterEdit := loadTemplateFile(t, e, path)
	for _, h := range []namedHandle{{"handle from the second Load", afterEdit}, {"handle from the first Load", beforeEdit}} {
		if got := Replace(h.handle, []string{"a", "1"}); got != "second 1" {
			t.Errorf("%s rendered %q after the edited file was loaded again, want %q", h.name, got, "second 1")
		}
	}
}

func TestBundleHandlesLoadedBeforeAnEditFollowLaterLoadsAndReloads(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.alos", "one {{a}}")
	writeFile(t, dir, "nav.alos", "nav one {{a}}")
	e := newTestEngine(t)
	bundleBefore := loadTemplateFile(t, e, dir)
	navBefore := bundleBefore.Named("nav")
	if navBefore == nil {
		t.Fatal("nav missing")
	}
	checkAll := func(stage string, bundles []*Template, navs []*Template, wantIndex string, wantNav string) {
		t.Helper()
		for i, bundle := range bundles {
			if got := Replace(bundle, []string{"a", "1"}); got != wantIndex {
				t.Errorf("%s: bundle handle %d rendered %q want %q", stage, i, got, wantIndex)
			}
		}
		for i, nav := range navs {
			if got := Replace(nav, []string{"a", "1"}); got != wantNav {
				t.Errorf("%s: nav handle %d rendered %q want %q", stage, i, got, wantNav)
			}
		}
	}

	writeFile(t, dir, "index.alos", "second {{a}}")
	writeFile(t, dir, "nav.alos", "nav second {{a}}")
	bundleAfter := loadTemplateFile(t, e, dir)
	navAfter := bundleAfter.Named("nav")
	if navAfter == nil {
		t.Fatal("nav missing after the second Load")
	}
	bundles := []*Template{bundleBefore, bundleAfter}
	navs := []*Template{navBefore, navAfter}
	checkAll("after the second Load", bundles, navs, "second 1", "nav second 1")

	writeFile(t, dir, "index.alos", "the third {{a}}")
	writeFile(t, dir, "nav.alos", "nav the third {{a}}")
	if err := e.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	checkAll("after Engine.Reload", bundles, navs, "the third 1", "nav the third 1")

	writeFile(t, dir, "index.alos", "fourth one {{a}}")
	writeFile(t, dir, "nav.alos", "nav fourth one {{a}}")
	if err := navBefore.Reload(); err != nil {
		t.Fatalf("nav Reload: %v", err)
	}
	checkAll("after the oldest nav handle reloaded", bundles, navs, "fourth one 1", "nav fourth one 1")
}

func TestHandlesLoadedDuringAReloadFollowLaterReloads(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "page.alos", "old {{a}}")
	e := newTestEngine(t)
	root := loadTemplateFile(t, e, path)

	var reloading sync.WaitGroup
	stop := make(chan struct{})
	runUntilStopped(&reloading, stop, func(int) {
		_ = root.Reload()
	})
	var failure firstFailure
	handles := make([][]*Template, stressWorkers)
	runWorkersFor(stressDuration, stressWorkers, func(worker int, _ int) {
		tpl, err := e.Load(path)
		if err != nil {
			failure.record("Load during reload failed: " + err.Error())
			return
		}
		if seen := handles[worker]; len(seen) == 0 || seen[len(seen)-1] != tpl {
			handles[worker] = append(seen, tpl)
		}
	})
	close(stop)
	reloading.Wait()
	failure.report(t)

	writeFile(t, dir, "page.alos", "brand new {{a}}")
	if err := e.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	distinct := map[*Template]bool{root: true}
	for _, seen := range handles {
		for _, tpl := range seen {
			distinct[tpl] = true
		}
	}
	stale := 0
	for tpl := range distinct {
		if Replace(tpl, []string{"a", "1"}) != "brand new 1" {
			stale++
		}
	}
	if stale != 0 {
		t.Fatalf("%d of %d handles returned by Load during a reload never received the later reload", stale, len(distinct))
	}
}

func TestTemplateRecoversAfterAReloadThatFailedToCompile(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "page.alos", "good {{a}}")
	e := newTestEngine(t)
	tpl := loadTemplateFile(t, e, path)

	writeFile(t, dir, "page.alos", "half written {{a")
	if err := e.Reload(); err == nil {
		t.Fatal("Reload of an unterminated placeholder reported no error")
	}
	if got := Replace(tpl, []string{"a", "1"}); got != "good 1" {
		t.Fatalf("render while the file is broken = %q want the last good page", got)
	}
	if _, err := e.Load(path); err == nil {
		t.Fatal("Load of an unterminated placeholder reported no error")
	}

	writeFile(t, dir, "page.alos", "repaired now {{a}}")
	if err := e.Reload(); err != nil {
		t.Fatalf("Reload after the file was repaired: %v", err)
	}
	if got := Replace(tpl, []string{"a", "1"}); got != "repaired now 1" {
		t.Fatalf("render after the file was repaired = %q", got)
	}
}

func TestEngineStaysUsableAfterStop(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "page.alos", "before {{a}}")
	other := writeFile(t, dir, "other.alos", "other {{a}}")
	e := NewEngine(WithAutoRefresh(stressRefreshPeriod))
	tpl := loadTemplateFile(t, e, path)
	e.Stop()
	e.Stop()

	if got := Replace(tpl, []string{"a", "1"}); got != "before 1" {
		t.Fatalf("render after Stop = %q", got)
	}
	late := loadTemplateFile(t, e, other)
	if got := ReplaceValues(late, []string{"1"}); got != "other 1" {
		t.Fatalf("render of a template loaded after Stop = %q", got)
	}
	writeFile(t, dir, "page.alos", "after the stop {{a}}")
	if err := e.Reload(); err != nil {
		t.Fatalf("Reload after Stop: %v", err)
	}
	if got := Replace(tpl, []string{"a", "1"}); got != "after the stop 1" {
		t.Fatalf("render after Stop and Reload = %q", got)
	}
	tpl.ClearRenderCache()
	e.SetAutoRefresh(stressRefreshPeriod)
	e.Stop()
}

func TestRenderCacheTTLIsNormalisedTheSameWayEverywhere(t *testing.T) {
	cases := []struct {
		given time.Duration
		want  time.Duration
	}{
		{RenderCacheDisabled, 0},
		{-time.Hour, 0},
		{time.Duration(math.MinInt64), 0},
		{0, DefaultRenderCacheTTL},
		{1, 1},
		{time.Hour, time.Hour},
		{largestRenderCacheTTL, largestRenderCacheTTL},
	}
	for _, tc := range cases {
		byOption := newTestEngine(t, WithRenderCache(tc.given))
		if got := byOption.RenderCacheTTL(); got != tc.want {
			t.Errorf("WithRenderCache(%d) gave %v want %v", tc.given, got, tc.want)
		}
		bySetter := newTestEngine(t)
		bySetter.SetRenderCacheTTL(tc.given)
		if got := bySetter.RenderCacheTTL(); got != tc.want {
			t.Errorf("SetRenderCacheTTL(%d) gave %v want %v", tc.given, got, tc.want)
		}
	}
}

func TestNilAndEmptyTemplatesAreSafeAcrossTheWholeAPI(t *testing.T) {
	var missing *Template
	for _, tpl := range []*Template{missing, {}} {
		if got := Replace(tpl, []string{"a", "1"}); got != "" {
			t.Errorf("Replace = %q", got)
		}
		if got := ReplaceMap(tpl, map[string]string{"a": "1"}); got != "" {
			t.Errorf("ReplaceMap = %q", got)
		}
		if got := ReplaceValues(tpl, []string{"1"}); got != "" {
			t.Errorf("ReplaceValues = %q", got)
		}
		kept := []byte("kept")
		if got := AppendPairs(kept, tpl, []string{"a", "1"}); string(got) != "kept" {
			t.Errorf("AppendPairs = %q", got)
		}
		if got := AppendMap(kept, tpl, map[string]string{"a": "1"}); string(got) != "kept" {
			t.Errorf("AppendMap = %q", got)
		}
		if got := AppendValues(kept, tpl, []string{"1"}); string(got) != "kept" {
			t.Errorf("AppendValues = %q", got)
		}
		if got := tpl.Keys(); len(got) != 0 {
			t.Errorf("Keys = %q", got)
		}
		if got := tpl.Names(); len(got) != 0 {
			t.Errorf("Names = %q", got)
		}
		if tpl.Name() != "" || tpl.FileName() != "" {
			t.Errorf("Name = %q FileName = %q", tpl.Name(), tpl.FileName())
		}
		if got := tpl.Named("anything"); got != nil {
			t.Errorf("Named returned a template")
		}
		tpl.ClearRenderCache()
		if err := tpl.Reload(); err == nil {
			t.Errorf("Reload of a template with no source succeeded")
		}
	}
}
