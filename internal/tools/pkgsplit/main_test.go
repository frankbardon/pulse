package main

import (
	"strings"
	"testing"
)

const (
	pub  = "example.com/m/enc"
	twin = "example.com/m/internal/enc"
)

func TestRewrite_SelectorsMethodsAndImports(t *testing.T) {
	src := `package p

import (
	"fmt"

	"example.com/m/enc"
)

func f(s *enc.Schema) {
	r := enc.NewReader(nil, s)
	p, _ := s.BuildPlan([]string{"a"})
	fmt.Println(r, p)
}
`
	out, changed, err := Rewrite([]byte(src), Options{
		From: pub, To: twin, Alias: "encx",
		Names:   map[string]bool{"NewReader": true},
		Methods: map[string]bool{"BuildPlan": true},
	})
	if err != nil || !changed {
		t.Fatalf("Rewrite: changed=%v err=%v", changed, err)
	}
	got := string(out)
	for _, want := range []string{
		`encx.NewReader(nil, s)`,
		`encx.BuildPlan(s, []string{"a"})`,
		`*enc.Schema`,
		`encx "example.com/m/internal/enc"`,
		`"example.com/m/enc"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
}

func TestRewrite_DropsUnusedFromImport(t *testing.T) {
	src := `package p

import "example.com/m/enc"

var r = enc.NewReader(nil, nil)
`
	out, _, err := Rewrite([]byte(src), Options{From: pub, To: twin, Alias: "encx", Names: map[string]bool{"NewReader": true}})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Contains(got, `"example.com/m/enc"`) || !strings.Contains(got, `encx "example.com/m/internal/enc"`) {
		t.Fatalf("imports not fixed:\n%s", got)
	}
}

func TestRewrite_ShadowedLocalUntouched(t *testing.T) {
	src := `package p

import "example.com/m/enc"

func f() {
	_ = enc.Schema{}
	enc := struct{ NewReader int }{}
	_ = enc.NewReader
}
`
	out, changed, err := Rewrite([]byte(src), Options{From: pub, To: twin, Alias: "encx", Names: map[string]bool{"NewReader": true}})
	if err != nil {
		t.Fatal(err)
	}
	if changed || string(out) != src {
		t.Fatalf("shadowed selector rewritten:\n%s", out)
	}
}

func TestRewrite_SamePackageMethodToFunc(t *testing.T) {
	src := `package enc

func (s *Schema) BuildPlan(r []string) (int, error) { return 0, nil }

func g(s *Schema) { _, _ = s.BuildPlan(nil) }
`
	out, _, err := Rewrite([]byte(src), Options{Methods: map[string]bool{"BuildPlan": true}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "BuildPlan(s, nil)") || !strings.Contains(string(out), "func BuildPlan(s *Schema, r []string) (int, error)") {
		t.Fatalf("method not converted:\n%s", out)
	}
}

func TestQualify_FreeRefsOnly(t *testing.T) {
	src := `package enc

import "io"

type Reader struct{ Schema *Schema }

func NewReader(r io.Reader, s *Schema) *Reader {
	_ = FieldTypeU8
	return &Reader{Schema: s}
}
`
	out, changed, err := Qualify([]byte(src), pub, "enc", map[string]bool{"Schema": true, "FieldTypeU8": true, "Reader": true})
	if err != nil || !changed {
		t.Fatalf("Qualify: changed=%v err=%v", changed, err)
	}
	got := string(out)
	for _, want := range []string{`Schema *enc.Schema`, `s *enc.Schema`, `_ = enc.FieldTypeU8`, `&Reader{Schema: s}`, `"example.com/m/enc"`} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
}

func TestMove_CutsDeclsWithDocsIntoNewFile(t *testing.T) {
	src := `package enc

import "io"

// Keep stays.
func Keep() {}

// Gone leaves.
func Gone(r io.Reader) {}

// M leaves too.
func (s *Schema) M() {}
`
	kept, moved, err := Move([]byte(src), nil, map[string]bool{"Gone": true, "Schema.M": true}, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(kept), "Gone") || strings.Contains(string(kept), "M leaves") || !strings.Contains(string(kept), "// Keep stays.") {
		t.Fatalf("src after move:\n%s", kept)
	}
	for _, want := range []string{"package enc", `import "io"`, "// Gone leaves.\nfunc Gone(r io.Reader) {}", "func (s *Schema) M() {}"} {
		if !strings.Contains(string(moved), want) {
			t.Errorf("dst lacks %q:\n%s", want, moved)
		}
	}
	if _, _, err := Move([]byte(src), nil, map[string]bool{"Nope": true}, ""); err == nil {
		t.Fatal("unknown name accepted")
	}
}
