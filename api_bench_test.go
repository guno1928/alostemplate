package alos

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

var (
	apiSinkBytes  []byte
	apiSinkTpl    *Template
	apiSinkErr    error
	apiSinkString string
	apiSinkEngine *Engine
	apiSinkOpt    Option
)

const apiSlots = 16

func apiSource() string {
	var b strings.Builder
	for i := 0; i < apiSlots; i++ {
		b.WriteString(strings.Repeat("x", 512))
		b.WriteString("{{k")
		b.WriteString(strconv.Itoa(i))
		b.WriteString("}}")
	}
	b.WriteString(strings.Repeat("x", 512))
	return b.String()
}

func apiTemplate(b *testing.B) (*Template, []string, map[string]string) {
	b.Helper()
	dir := b.TempDir()
	path := filepath.Join(dir, "page.alos")
	if err := os.WriteFile(path, []byte(apiSource()), 0o600); err != nil {
		b.Fatal(err)
	}
	tpl, err := Load(path)
	if err != nil {
		b.Fatal(err)
	}
	pairs := make([]string, 0, apiSlots*2)
	values := make(map[string]string, apiSlots)
	for i := 0; i < apiSlots; i++ {
		k := "k" + strconv.Itoa(i)
		v := "value-" + strconv.Itoa(i)
		pairs = append(pairs, k, v)
		values[k] = v
	}
	return tpl, pairs, values
}

func BenchmarkAPI_New(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		e := New()
		apiSinkEngine = e
		e.Stop()
	}
}

func BenchmarkAPI_WithDelimiters(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		apiSinkOpt = WithDelimiters("<%", "%>")
	}
}

func BenchmarkAPI_WithAutoRefresh(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		apiSinkOpt = WithAutoRefresh(time.Minute)
	}
}

func BenchmarkAPI_WithModifiedOnly(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		apiSinkOpt = WithModifiedOnly(true)
	}
}

func BenchmarkAPI_Load_Cached(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "page.alos")
	if err := os.WriteFile(path, []byte(apiSource()), 0o600); err != nil {
		b.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		apiSinkTpl, apiSinkErr = Load(path)
	}
}

func BenchmarkAPI_Reload(b *testing.B) {
	if _, _, _ = apiTemplate(b); true {
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		apiSinkErr = Reload()
	}
}

func BenchmarkAPI_SetDelimiters(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		SetDelimiters("{{", "}}")
	}
}

func BenchmarkAPI_SetAutoRefresh(b *testing.B) {
	b.ReportAllocs()
	b.Cleanup(func() { SetAutoRefresh(0) })
	for i := 0; i < b.N; i++ {
		SetAutoRefresh(0)
	}
}

func BenchmarkAPI_Delimiters(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		apiSinkString, _ = Delimiters()
	}
}

func BenchmarkAPI_Stop(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		e := New()
		b.StartTimer()
		e.Stop()
	}
}

func BenchmarkAPI_Replace_Pairs_FreshBuffer(b *testing.B) {
	tpl, pairs, _ := apiTemplate(b)
	warm, err := Replace(tpl, pairs)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(warm)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		apiSinkString, apiSinkErr = Replace(tpl, pairs)
	}
}

func BenchmarkAPI_Replace_Pairs_ReusedBuffer(b *testing.B) {
	tpl, pairs, _ := apiTemplate(b)
	dst, err := Replace(tpl, pairs)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(dst)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dst, apiSinkErr = Replace(tpl, pairs)
	}
	apiSinkString = dst
}

func BenchmarkAPI_Replace_Map_FreshBuffer(b *testing.B) {
	tpl, _, values := apiTemplate(b)
	warm, err := Replace(tpl, values)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(warm)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		apiSinkString, apiSinkErr = Replace(tpl, values)
	}
}

func BenchmarkAPI_Replace_Map_ReusedBuffer(b *testing.B) {
	tpl, _, values := apiTemplate(b)
	dst, err := Replace(tpl, values)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(dst)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dst, apiSinkErr = Replace(tpl, values)
	}
	apiSinkString = dst
}

func BenchmarkAPI_Replace_StringShorthand(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "single.alos")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 512)+"{{k0}}"+strings.Repeat("x", 512)), 0o600); err != nil {
		b.Fatal(err)
	}
	tpl, err := Load(path)
	if err != nil {
		b.Fatal(err)
	}
	dst, err := Replace(tpl, "value")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(dst)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dst, apiSinkErr = Replace(tpl, "value")
	}
	apiSinkString = dst
}
