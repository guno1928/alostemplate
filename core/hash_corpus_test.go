package core

import (
	"strconv"
	"strings"
	"testing"
)

const (
	corpusLongestItem      = 400
	corpusFlipLongestItem  = 200
	corpusThreeByteSamples = 32
	corpusThreeByteSpacing = 8
	corpusSplitText        = "0123456789abcdefghijklmn"
	corpusEmptyItemLists   = 12
)

type hashCorpus struct {
	t      *testing.T
	seed   uint64
	seen   map[uint64]string
	inputs int
}

func newHashCorpus(t *testing.T, seed uint64) *hashCorpus {
	return &hashCorpus{t: t, seed: seed, seen: make(map[uint64]string, 1<<18)}
}

func describeItems(items []string) string {
	var b strings.Builder
	b.WriteString(strconv.Itoa(len(items)))
	for _, item := range items {
		b.WriteByte('|')
		b.WriteString(strconv.Itoa(len(item)))
		b.WriteByte(':')
		b.WriteString(item)
	}
	return b.String()
}

func (c *hashCorpus) add(items ...string) {
	c.t.Helper()
	identity := describeItems(items)
	h := hashStrings(c.seed, items)
	if previous, ok := c.seen[h]; ok {
		if previous != identity {
			c.t.Fatalf("hash collision between %q and %q", previous, identity)
		}
		return
	}
	c.seen[h] = identity
	c.inputs++
}

func TestHashStringsHasNoCollisionsAcrossAStructuredCorpus(t *testing.T) {
	corpus := newHashCorpus(t, valuesHashSeed)

	corpus.add()
	for count := 1; count <= corpusEmptyItemLists; count++ {
		corpus.add(make([]string, count)...)
	}

	for first := 0; first < 256; first++ {
		corpus.add(string([]byte{byte(first)}))
		for second := 0; second < 256; second++ {
			corpus.add(string([]byte{byte(first), byte(second)}))
		}
	}
	for first := 0; first < 256; first++ {
		for second := 0; second < corpusThreeByteSamples; second++ {
			for third := 0; third < corpusThreeByteSamples; third++ {
				corpus.add(string([]byte{byte(first), byte(second * corpusThreeByteSpacing), byte(third*corpusThreeByteSpacing + 1)}))
			}
		}
	}

	for length := 0; length <= corpusLongestItem; length++ {
		corpus.add(strings.Repeat("a", length))
		corpus.add(string(patternedValue(length)))
	}

	for length := 1; length <= corpusFlipLongestItem; length++ {
		base := patternedValue(length)
		for position := range base {
			for _, mask := range []byte{0x01, 0x80} {
				flipped := append([]byte(nil), base...)
				flipped[position] ^= mask
				corpus.add(string(flipped))
			}
		}
	}

	for first := 0; first <= len(corpusSplitText); first++ {
		corpus.add(corpusSplitText[:first], corpusSplitText[first:])
		for second := first; second <= len(corpusSplitText); second++ {
			corpus.add(corpusSplitText[:first], corpusSplitText[first:second], corpusSplitText[second:])
		}
	}

	for id := range 50000 {
		user := "user-" + strconv.Itoa(id)
		corpus.add(user, user+"@example.test", strconv.Itoa(id))
		corpus.add("user", user)
	}

	if corpus.inputs < 300000 {
		t.Fatalf("corpus only covered %d distinct inputs", corpus.inputs)
	}
}

func TestHashStringsSeparatesTheThreeInputForms(t *testing.T) {
	for id := range 2000 {
		items := []string{"k" + strconv.Itoa(id), strings.Repeat("v", id%70)}
		pairs := hashStrings(pairsHashSeed, items)
		values := hashStrings(valuesHashSeed, items)
		mapped := hashStrings(mapHashSeed, items)
		if pairs == values || pairs == mapped || values == mapped {
			t.Fatalf("input forms share a cache key for %q", items)
		}
	}
}

func TestMapHashIgnoresInsertionOrderAndChunkBoundaries(t *testing.T) {
	e := newTestEngine(t)
	for _, slots := range []int{1, 15, 16, 17, 31, 32, 33, 100} {
		tpl := mustCompile(t, e, buildSource(slots, 2, "k"))
		forward := make(map[string]string, slots)
		backward := make(map[string]string, slots)
		for i := range slots {
			forward["k"+strconv.Itoa(i)] = "value" + strconv.Itoa(i)
		}
		for i := slots - 1; i >= 0; i-- {
			backward["k"+strconv.Itoa(i)] = "value" + strconv.Itoa(i)
		}
		want := tpl.hashMapValues(forward)
		if got := tpl.hashMapValues(backward); got != want {
			t.Fatalf("slots=%d: insertion order changed the map hash", slots)
		}
		for i := range slots {
			key := "k" + strconv.Itoa(i)
			changed := make(map[string]string, slots)
			for k, v := range forward {
				changed[k] = v
			}
			changed[key] = "other"
			if tpl.hashMapValues(changed) == want {
				t.Fatalf("slots=%d: changing %s did not change the map hash", slots, key)
			}
			delete(changed, key)
			if tpl.hashMapValues(changed) == want {
				t.Fatalf("slots=%d: removing %s did not change the map hash", slots, key)
			}
			changed[key] = ""
			if tpl.hashMapValues(changed) == want {
				t.Fatalf("slots=%d: emptying %s did not change the map hash", slots, key)
			}
		}
	}
}
