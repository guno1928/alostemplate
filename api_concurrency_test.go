package alos

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guno1928/turbo"
)

const (
	apiStressDuration = 150 * time.Millisecond
	apiStressWorkers  = 16
	apiStressAccounts = 64
	apiPageSource     = "<h1>{{user}}</h1><p>{{plan}}</p>"
)

type apiAccount struct {
	lookup  map[string]string
	pairs   []string
	inOrder []string
	want    string
}

func buildAPIAccount(id int) apiAccount {
	user := "user-" + strconv.Itoa(id)
	plan := "plan-" + strconv.Itoa(id%7)
	return apiAccount{
		lookup:  map[string]string{"user": user, "plan": plan},
		pairs:   []string{"user", user, "plan", plan},
		inOrder: []string{user, plan},
		want:    "<h1>" + user + "</h1><p>" + plan + "</p>",
	}
}

func TestPackageAPIServesEachAccountItsOwnPageUnderConcurrency(t *testing.T) {
	path := writeTemplate(t, t.TempDir(), "account.alos", apiPageSource)
	tpl, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Cleanup(func() { SetRenderCacheTTL(DefaultRenderCacheTTL) })
	accounts := make([]apiAccount, apiStressAccounts)
	for id := range accounts {
		accounts[id] = buildAPIAccount(id)
	}

	var failed atomic.Pointer[string]
	record := func(message string) { failed.CompareAndSwap(nil, &message) }
	check := func(id int, form string, got string, err error) {
		if err != nil {
			record("account " + strconv.Itoa(id) + " " + form + " failed: " + err.Error())
		} else if got != accounts[id].want {
			record("account " + strconv.Itoa(id) + " " + form + " rendered " + got)
		}
	}

	deadline := turbo.Deadline(apiStressDuration)
	var group sync.WaitGroup
	for worker := range apiStressWorkers {
		group.Go(func() {
			var buf []byte
			for iteration := 0; turbo.Until(deadline) > 0; iteration++ {
				id := (worker*7 + iteration) % apiStressAccounts
				account := accounts[id]
				switch iteration % 7 {
				case 0:
					got, err := Replace(tpl, account.lookup)
					check(id, "Replace map", got, err)
				case 1:
					got, err := Replace(tpl, account.pairs)
					check(id, "Replace pairs", got, err)
				case 2:
					check(id, "ReplaceValues", ReplaceValues(tpl, account.inOrder), nil)
				case 3:
					out, err := AppendReplace(buf[:0], tpl, account.lookup)
					buf = out
					check(id, "AppendReplace map", string(out), err)
				case 4:
					out, err := AppendReplace(buf[:0], tpl, account.pairs)
					buf = out
					check(id, "AppendReplace pairs", string(out), err)
				case 5:
					buf = AppendValues(buf[:0], tpl, account.inOrder)
					check(id, "AppendValues", string(buf), nil)
				default:
					reloaded, err := Load(path)
					if err != nil {
						record("Load failed: " + err.Error())
						continue
					}
					check(id, "render of a reloaded handle", ReplaceValues(reloaded, account.inOrder), nil)
				}
			}
		})
	}
	group.Go(func() {
		lifetimes := []time.Duration{DefaultRenderCacheTTL, RenderCacheDisabled, time.Millisecond}
		for iteration := 0; turbo.Until(deadline) > 0; iteration++ {
			SetRenderCacheTTL(lifetimes[iteration%len(lifetimes)])
			_ = RenderCacheTTL()
			tpl.ClearRenderCache()
			if err := Reload(); err != nil {
				record("Reload failed: " + err.Error())
			}
		}
	})
	group.Wait()
	if message := failed.Load(); message != nil {
		t.Fatal(*message)
	}
}

func TestEveryPublicInputFormAgreesAcrossRepeatedCachedCalls(t *testing.T) {
	tpl := loadAppendTemplate(t, apiPageSource)
	for id := range apiStressAccounts {
		account := buildAPIAccount(id)
		for call := range 4 {
			forms := map[string]any{
				"map":   account.lookup,
				"pairs": account.pairs,
			}
			for name, values := range forms {
				got, err := Replace(tpl, values)
				if err != nil || got != account.want {
					t.Fatalf("account %d call %d Replace %s = %q, %v", id, call, name, got, err)
				}
				out, err := AppendReplace(nil, tpl, values)
				if err != nil || string(out) != account.want {
					t.Fatalf("account %d call %d AppendReplace %s = %q, %v", id, call, name, out, err)
				}
			}
			if got := ReplaceValues(tpl, account.inOrder); got != account.want {
				t.Fatalf("account %d call %d ReplaceValues = %q", id, call, got)
			}
		}
	}
}
