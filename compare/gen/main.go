package main

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"

	compare "github.com/guno1928/alostemplate/compare"
)

func write(path string, content string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		panic(err)
	}
}

func rustString(s string) string {
	return "r####\"" + s + "\"####"
}

func main() {
	var rust strings.Builder
	rust.WriteString("use sailfish::TemplateSimple;\n\n")
	rust.WriteString("pub struct Case {\n    pub name: &'static str,\n    pub values: Vec<&'static str>,\n    pub expected_len: usize,\n    pub expected_hash: u64,\n    pub render: fn(&[&str]) -> String,\n    pub render_to: fn(&[&str], &mut sailfish::runtime::Buffer),\n}\n\n")
	var cases strings.Builder
	for _, s := range compare.Scenarios {
		lower := strings.ToLower(s.Name)
		write(filepath.Join("qt", lower+".qtpl"), s.QuickTemplateSource())
		write(filepath.Join("rust", "templates", lower+".stpl"), s.SailfishSource())
		expected := s.Expected()
		h := fnv.New64a()
		h.Write([]byte(expected))
		fmt.Fprintf(&rust, "#[derive(TemplateSimple)]\n#[template(path = \"%s.stpl\")]\nstruct %s<'a> {\n    v: &'a [&'a str],\n}\n\n", lower, s.Name)
		fmt.Fprintf(&rust, "fn render_%s(v: &[&str]) -> String {\n    %s { v }.render_once().unwrap()\n}\n\n", lower, s.Name)
		fmt.Fprintf(&rust, "fn render_%s_to(v: &[&str], buf: &mut sailfish::runtime::Buffer) {\n    %s { v }.render_once_to(buf).unwrap()\n}\n\n", lower, s.Name)
		values := make([]string, 0, s.Slots)
		for _, v := range s.Values() {
			values = append(values, rustString(v))
		}
		fmt.Fprintf(&cases, "        Case {\n            name: \"%s\",\n            values: vec![%s],\n            expected_len: %d,\n            expected_hash: %d,\n            render: render_%s,\n            render_to: render_%s_to,\n        },\n", s.Name, strings.Join(values, ", "), len(expected), h.Sum64(), lower, lower)
	}
	rust.WriteString("pub fn cases() -> Vec<Case> {\n    vec![\n" + cases.String() + "    ]\n}\n")
	write(filepath.Join("rust", "src", "lib.rs"), rust.String())
}
