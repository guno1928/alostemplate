package compare

import (
	"strconv"
	"strings"
)

type Scenario struct {
	Name       string
	Slots      int
	LiteralLen int
	ValueLen   int
}

var Scenarios = []Scenario{
	{Name: "Tiny", Slots: 1, LiteralLen: 8, ValueLen: 5},
	{Name: "Small", Slots: 4, LiteralLen: 40, ValueLen: 12},
	{Name: "Medium", Slots: 16, LiteralLen: 128, ValueLen: 24},
	{Name: "Large", Slots: 64, LiteralLen: 512, ValueLen: 32},
	{Name: "Huge", Slots: 8, LiteralLen: 16384, ValueLen: 64},
	{Name: "LongValues", Slots: 8, LiteralLen: 64, ValueLen: 4096},
	{Name: "ManySlots", Slots: 256, LiteralLen: 16, ValueLen: 8},
}

const literalSeed = `<div class="card"><h2>Account overview</h2><p>Your balance and recent activity are shown below.</p></div>`

const valueSeed = "Alexandria-Quartz-0123456789-"

func repeatTo(seed string, offset int, n int) string {
	var b strings.Builder
	b.Grow(n)
	for b.Len() < n {
		start := (offset + b.Len()) % len(seed)
		chunk := seed[start:]
		if remaining := n - b.Len(); len(chunk) > remaining {
			chunk = chunk[:remaining]
		}
		b.WriteString(chunk)
	}
	return trimEdgeSpaces(b.String())
}

func trimEdgeSpaces(s string) string {
	raw := []byte(s)
	if len(raw) != 0 && raw[0] == ' ' {
		raw[0] = '_'
	}
	if len(raw) != 0 && raw[len(raw)-1] == ' ' {
		raw[len(raw)-1] = '_'
	}
	return string(raw)
}

func (s Scenario) Literal(i int) string {
	return repeatTo(literalSeed, i*7, s.LiteralLen)
}

func (s Scenario) Key(i int) string {
	return "k" + strconv.Itoa(i)
}

func (s Scenario) Value(i int) string {
	return repeatTo(valueSeed, i*3, s.ValueLen)
}

func (s Scenario) Values() []string {
	values := make([]string, s.Slots)
	for i := range values {
		values[i] = s.Value(i)
	}
	return values
}

func (s Scenario) Pairs() []string {
	pairs := make([]string, 0, s.Slots*2)
	for i := 0; i < s.Slots; i++ {
		pairs = append(pairs, s.Key(i), s.Value(i))
	}
	return pairs
}

func (s Scenario) Map() map[string]string {
	values := make(map[string]string, s.Slots)
	for i := 0; i < s.Slots; i++ {
		values[s.Key(i)] = s.Value(i)
	}
	return values
}

func (s Scenario) AnyMap() map[string]interface{} {
	values := make(map[string]interface{}, s.Slots)
	for i := 0; i < s.Slots; i++ {
		values[s.Key(i)] = s.Value(i)
	}
	return values
}

func (s Scenario) source(slot func(i int) string) string {
	var b strings.Builder
	for i := 0; i < s.Slots; i++ {
		b.WriteString(s.Literal(i))
		b.WriteString(slot(i))
	}
	b.WriteString(s.Literal(s.Slots))
	return b.String()
}

func (s Scenario) BraceSource() string {
	return s.source(func(i int) string { return "{{" + s.Key(i) + "}}" })
}

func (s Scenario) GoTemplateSource() string {
	return s.source(func(i int) string { return "{{." + s.Key(i) + "}}" })
}

func (s Scenario) QuickTemplateSource() string {
	body := s.source(func(i int) string { return "{%s= v[" + strconv.Itoa(i) + "] %}" })
	return "{% func " + s.Name + "(v []string) %}" + body + "{% endfunc %}\n"
}

func (s Scenario) SailfishSource() string {
	return s.source(func(i int) string { return "<%- self.v[" + strconv.Itoa(i) + "] %>" })
}

func (s Scenario) Expected() string {
	return s.source(func(i int) string { return s.Value(i) })
}
