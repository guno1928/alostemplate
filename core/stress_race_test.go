package core

import (
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guno1928/turbo"
)

const (
	stressDuration      = 150 * time.Millisecond
	stressWorkers       = 16
	stressAccounts      = 256
	stressHotAccounts   = 8
	stressRenderForms   = 6
	stressShortTTL      = 2 * time.Millisecond
	pooledLinearSlots   = 40
	pooledRepeatedKeys  = 24
	pooledValueVariants = 5
	stressRefreshPeriod = 5 * time.Millisecond
	stopAfter           = 50 * time.Millisecond
	bundleFileCount     = 3
)

type firstFailure struct {
	message atomic.Pointer[string]
}

func (f *firstFailure) record(message string) {
	f.message.CompareAndSwap(nil, &message)
}

func (f *firstFailure) report(t *testing.T) {
	t.Helper()
	if message := f.message.Load(); message != nil {
		t.Fatal(*message)
	}
}

func runWorkersFor(duration time.Duration, workers int, work func(worker int, iteration int)) {
	deadline := turbo.Deadline(duration)
	var group sync.WaitGroup
	for worker := range workers {
		group.Go(func() {
			for iteration := 0; turbo.Until(deadline) > 0; iteration++ {
				work(worker, iteration)
			}
		})
	}
	group.Wait()
}

func runUntilStopped(group *sync.WaitGroup, stop <-chan struct{}, work func(iteration int)) {
	group.Go(func() {
		for iteration := 0; ; iteration++ {
			select {
			case <-stop:
				return
			default:
			}
			work(iteration)
			runtime.Gosched()
		}
	})
}

func pickAccount(worker int, iteration int) int {
	if iteration%2 == 0 {
		return (worker + iteration) % stressHotAccounts
	}
	return turbo.Intn(stressAccounts)
}

func TestConcurrentCachedRendersNeverCrossAccounts(t *testing.T) {
	e := newTestEngine(t)
	tpl := mustCompile(t, e, accountPageSource)
	pages := make([]accountPage, stressAccounts)
	for id := range pages {
		pages[id] = buildAccountPage(id)
	}
	buffers := make([][]byte, stressWorkers)

	var failure firstFailure
	var disruptors sync.WaitGroup
	stop := make(chan struct{})
	runUntilStopped(&disruptors, stop, func(int) {
		tpl.ClearRenderCache()
	})
	lifetimes := []time.Duration{DefaultRenderCacheTTL, RenderCacheDisabled, stressShortTTL, time.Hour}
	runUntilStopped(&disruptors, stop, func(iteration int) {
		e.SetRenderCacheTTL(lifetimes[iteration%len(lifetimes)])
		_ = e.RenderCacheTTL()
	})

	runWorkersFor(stressDuration, stressWorkers, func(worker int, iteration int) {
		id := pickAccount(worker, iteration)
		page := pages[id]
		var got string
		form := iteration % stressRenderForms
		switch form {
		case 0:
			got = ReplaceMap(tpl, page.values)
		case 1:
			got = Replace(tpl, page.pairs)
		case 2:
			got = ReplaceValues(tpl, page.inOrder)
		case 3:
			got = string(AppendMap(nil, tpl, page.values))
		case 4:
			buffers[worker] = AppendPairs(buffers[worker][:0], tpl, page.pairs)
			got = string(buffers[worker])
		default:
			buffers[worker] = AppendValues(buffers[worker][:0], tpl, page.inOrder)
			got = string(buffers[worker])
		}
		if got != page.want {
			failure.record("account " + strconv.Itoa(id) + " form " + strconv.Itoa(form) + " rendered " + got)
		}
	})
	close(stop)
	disruptors.Wait()
	failure.report(t)
}

func repeatedKeySource(keys int) string {
	var b strings.Builder
	for i := range keys {
		b.WriteString("<{{r")
		b.WriteString(strconv.Itoa(i))
		b.WriteString("}}>")
	}
	for i := keys - 1; i >= 0; i-- {
		b.WriteString("[{{r")
		b.WriteString(strconv.Itoa(i))
		b.WriteString("}}]")
	}
	return b.String()
}

type pooledValueSet struct {
	lookup        map[string]string
	shuffled      []string
	inOrder       []string
	want          string
	wantFirstHalf string
}

func buildPooledValueSet(source string, keys []string, worker int, variant int) pooledValueSet {
	set := pooledValueSet{lookup: make(map[string]string, len(keys))}
	firstHalf := make(map[string]string, len(keys)/2)
	for i, key := range keys {
		value := "w" + strconv.Itoa(worker) + "-" + key + "-" + strings.Repeat("v", variant*7+i%5)
		set.lookup[key] = value
		set.inOrder = append(set.inOrder, value)
		if i < len(keys)/2 {
			firstHalf[key] = value
		}
	}
	for i := len(keys) - 1; i >= 0; i-- {
		set.shuffled = append(set.shuffled, keys[i], set.lookup[keys[i]])
	}
	set.want = expectedRender(source, set.lookup)
	set.wantFirstHalf = expectedRender(source, firstHalf)
	return set
}

func TestConcurrentRendersThroughThePooledScratchStayCorrect(t *testing.T) {
	sources := []string{buildSource(pooledLinearSlots, 3, "k"), repeatedKeySource(pooledRepeatedKeys)}
	engines := []*Engine{newTestEngine(t), newTestEngine(t, WithRenderCache(RenderCacheDisabled))}
	for _, source := range sources {
		for _, e := range engines {
			tpl := mustCompile(t, e, source)
			keys := tpl.Keys()
			sets := make([][]pooledValueSet, stressWorkers)
			for worker := range sets {
				for variant := range pooledValueVariants {
					sets[worker] = append(sets[worker], buildPooledValueSet(source, keys, worker, variant))
				}
			}
			var failure firstFailure
			runWorkersFor(stressDuration/4, stressWorkers, func(worker int, iteration int) {
				set := sets[worker][iteration%pooledValueVariants]
				half := len(keys) / 2
				checks := []struct {
					name string
					got  string
					want string
				}{
					{"shuffled pairs", Replace(tpl, set.shuffled), set.want},
					{"map", ReplaceMap(tpl, set.lookup), set.want},
					{"values", ReplaceValues(tpl, set.inOrder), set.want},
					{"short values", ReplaceValues(tpl, set.inOrder[:half]), set.wantFirstHalf},
					{"append shuffled pairs", string(AppendPairs(nil, tpl, set.shuffled)), set.want},
					{"append map", string(AppendMap(nil, tpl, set.lookup)), set.want},
					{"append short values", string(AppendValues(nil, tpl, set.inOrder[:half])), set.wantFirstHalf},
				}
				for _, check := range checks {
					if check.got != check.want {
						failure.record("worker " + strconv.Itoa(worker) + " " + check.name + " rendered " + check.got)
					}
				}
			})
			failure.report(t)
		}
	}
}

func TestSetDelimitersWhileReloadingIsRaceFree(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "page.alos", "{{x}} <%x%>")
	e := newTestEngine(t)
	tpl, err := e.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var background sync.WaitGroup
	stop := make(chan struct{})
	runUntilStopped(&background, stop, func(iteration int) {
		if iteration%2 == 0 {
			e.SetDelimiters("<%", "%>")
		} else {
			e.SetDelimiters("{{", "}}")
		}
	})
	runWorkersFor(stressDuration, 4, func(int, int) {
		_ = tpl.Reload()
		_, _ = e.Delimiters()
		_ = Replace(tpl, []string{"x", "1"})
	})
	close(stop)
	background.Wait()
}

func TestStopWhileLoadingReloadingAndRenderingIsRaceFree(t *testing.T) {
	dir := t.TempDir()
	paths := make([]string, bundleFileCount)
	for i := range paths {
		paths[i] = writeFile(t, dir, "page"+strconv.Itoa(i)+".alos", "P"+strconv.Itoa(i)+"-{{v}}")
	}
	e := NewEngine(WithAutoRefresh(stressRefreshPeriod))
	loaded := make([]*Template, len(paths))
	for i, path := range paths {
		tpl, err := e.Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		loaded[i] = tpl
	}

	var failure firstFailure
	var stopper sync.WaitGroup
	stopper.Go(func() {
		turbo.Sleep(stopAfter)
		e.Stop()
		e.Stop()
	})
	runWorkersFor(stressDuration, stressWorkers, func(worker int, iteration int) {
		index := (worker + iteration) % len(paths)
		want := "P" + strconv.Itoa(index) + "-x"
		if got := Replace(loaded[index], []string{"v", "x"}); got != want {
			failure.record("render after stop = " + got + " want " + want)
		}
		switch iteration % 3 {
		case 0:
			if tpl, err := e.Load(paths[index]); err == nil {
				if got := ReplaceValues(tpl, []string{"x"}); got != want {
					failure.record("render of a template loaded around stop = " + got + " want " + want)
				}
			}
		case 1:
			_ = e.Reload()
		default:
			_ = loaded[index].Reload()
		}
	})
	stopper.Wait()
	failure.report(t)
}

func TestBundleAccessorsWhileReloadingAndTogglingRefreshAreRaceFree(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.alos", "I0-{{v}}-{{include \"footer\"}}")
	writeFile(t, dir, "nav.alos", "N0-{{v}}")
	writeFile(t, dir, "footer.alos", "F0")
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
	wellFormed := func(got string, prefix string, suffix func(string) string) bool {
		latest := version.Load()
		for v := int64(0); v <= latest+1; v++ {
			s := strconv.FormatInt(v, 10)
			if got == prefix+s+"-x"+suffix(s) {
				return true
			}
		}
		return false
	}
	indexSuffix := func(s string) string { return "-F" + s }
	navSuffix := func(string) string { return "" }

	var failure firstFailure
	var readers sync.WaitGroup
	stop := make(chan struct{})
	for range stressWorkers / 2 {
		runUntilStopped(&readers, stop, func(iteration int) {
			if got := ReplaceValues(bundle, []string{"x"}); !wellFormed(got, "I", indexSuffix) {
				failure.record("bundle rendered " + got)
			}
			if got := string(AppendValues(nil, nav, []string{"x"})); !wellFormed(got, "N", navSuffix) {
				failure.record("nav rendered " + got)
			}
			if keys := bundle.Keys(); len(keys) != 1 || keys[0] != "v" {
				failure.record("bundle keys = " + strings.Join(keys, ","))
			}
			if names := bundle.Names(); len(names) != bundleFileCount {
				failure.record("bundle names = " + strings.Join(names, ","))
			}
			if bundle.Named("FOOTER.alos") == nil || bundle.Named("") == nil {
				failure.record("Named lost a child during reload")
			}
			if bundle.Name() != "index" || bundle.FileName() != "index.alos" || nav.Name() != "nav" {
				failure.record("Name or FileName changed during reload")
			}
			if iteration%4 == 0 {
				nav.ClearRenderCache()
				bundle.ClearRenderCache()
			}
		})
	}
	runUntilStopped(&readers, stop, func(iteration int) {
		e.SetAutoRefresh(time.Duration(iteration%2) * stressRefreshPeriod)
		_ = e.AutoRefresh()
	})

	deadline := turbo.Deadline(stressDuration)
	for v := int64(1); turbo.Until(deadline) > 0; v++ {
		s := strconv.FormatInt(v, 10)
		e.reloadMu.Lock()
		version.Store(v - 1)
		writeFile(t, dir, "index.alos", "I"+s+"-{{v}}-{{include \"footer\"}}")
		writeFile(t, dir, "nav.alos", "N"+s+"-{{v}}")
		writeFile(t, dir, "footer.alos", "F"+s)
		version.Store(v)
		e.reloadMu.Unlock()
		var reloadErr error
		switch v % 3 {
		case 0:
			reloadErr = e.Reload()
		case 1:
			reloadErr = bundle.Reload()
		default:
			reloadErr = nav.Reload()
		}
		if reloadErr != nil {
			failure.record("reload failed: " + reloadErr.Error())
		}
	}
	close(stop)
	readers.Wait()
	e.SetAutoRefresh(0)
	failure.report(t)

	if err := e.Reload(); err != nil {
		t.Fatalf("final Reload: %v", err)
	}
	final := strconv.FormatInt(version.Load(), 10)
	if got := ReplaceValues(bundle, []string{"x"}); got != "I"+final+"-x-F"+final {
		t.Fatalf("bundle after reloads = %q", got)
	}
	if got := ReplaceValues(nav, []string{"x"}); got != "N"+final+"-x" {
		t.Fatalf("nav after reloads = %q", got)
	}
}
