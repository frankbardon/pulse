package main

import (
	"strings"
	"testing"
)

// sectionSites maps a report's sites of one section class to their
// "path symbol" keys and notes.
func sectionSites(rep *Report, class string) map[string]string {
	out := map[string]string{}
	for _, sec := range rep.Sections {
		if sec.Class != class {
			continue
		}
		for _, s := range sec.Sites {
			if len(s.Symbols) == 0 {
				out[s.Path] = s.Coverage + " " + s.Note
				continue
			}
			for _, sym := range s.Symbols {
				out[s.Path+" "+sym.Name] = s.Coverage + " " + sym.Note
			}
		}
	}
	return out
}

func hasKey(m map[string]string, k string) bool { _, ok := m[k]; return ok }

// fieldTypeTree is a miniature tree with one site (and one near miss)
// per field-type rule. Types: u8, f64, date, decimal128.
var fieldTypeTree = map[string]string{
	"encoding/field_type.go": `package encoding

type FieldType byte

const (
	FieldTypeU8 FieldType = iota
	FieldTypeF64
	FieldTypeDate
	FieldTypeDecimal128
)

func (ft FieldType) String() string {
	switch ft {
	case FieldTypeU8:
		return "u8"
	case FieldTypeF64:
		return "f64"
	case FieldTypeDate:
		return "date"
	case FieldTypeDecimal128:
		return "decimal128"
	}
	return ""
}

// IsDecimal names one type: a selective predicate.
func (ft FieldType) IsDecimal() bool { return ft == FieldTypeDecimal128 }
`,
	"internal/codec/width.go": `package codec

import "github.com/frankbardon/pulse/encoding"

// width is a complete table that skips decimal128.
func width(t encoding.FieldType) int {
	switch t {
	case encoding.FieldTypeU8:
		return 1
	case encoding.FieldTypeF64, encoding.FieldTypeDate:
		return 8
	}
	return 0
}

// near miss: names every type only in this comment —
// encoding.FieldTypeU8 encoding.FieldTypeF64 encoding.FieldTypeDate
func quiet() {}
`,
	"internal/facet/rule.go": `package facet

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

func isNumeric(t encoding.FieldType) bool {
	return t == encoding.FieldTypeDecimal128
}

func Refusal(t encoding.FieldType) *errors.CodedError {
	if isNumeric(t) {
		return nil
	}
	return nil
}

func predictFacet(t encoding.FieldType) { _ = Refusal(t) }
`,
	"internal/service/facet.go": `package service

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/facet"
)

func runFacet(t encoding.FieldType) { _ = facet.Refusal(t) }
`,
	"internal/engine/cells.go": `package engine

func decimalCellFloat(v any) (float64, bool) { return 0, false }

func toFloat64(v any) float64 { f, _ := decimalCellFloat(v); return f }

// decimalSum carries the word itself: no generic reader.
func decimalSum(v any) float64 { f, _ := decimalCellFloat(v); return f }
`,
	"internal/engine/post.go": `package engine

func floatValue(v any) float64 { f, _ := decimalCellFloat(v); return f }
`,
	"internal/engine/group.go": `package engine

import "github.com/frankbardon/pulse/encoding"

func keyFor(t encoding.FieldType, v float64) string {
	if t.IsDecimal() {
		return formatKey(v)
	}
	return ""
}

func formatKey(v float64) string { return "" }
`,
	"internal/other/fmt.go": `package other

// near miss: a float formatter in a file with no field-type dispatch
func formatX(v float64) string { return "" }
`,
	"docs/src/format/field-types.md": "# Field types\n\n## The catalog\n\n| Type | Bytes |\n|---|---|\n" +
		"| `u8` | 1 |\n| `f64` | 8 |\n| `date` | 4 |\n| `decimal128` | 16 |\n\n" +
		"## Prose\n\nA `date` is not a `decimal128`.\n",
	"internal/skills/type-decimal128.md": "# decimal128\n",
}

func TestFieldTypeReport_Rules(t *testing.T) {
	t.Parallel()
	r := fixtureRepo(t, fieldTypeTree)
	f, err := r.loadFieldTypes()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, m := range f.Members {
		names = append(names, m.Name)
	}
	if got := strings.Join(names, ","); got != "u8,f64,date,decimal128" {
		t.Fatalf("members = %s", got)
	}

	rep, err := fieldTypeReport(r, "decimal128", options{})
	if err != nil {
		t.Fatal(err)
	}
	tables := sectionSites(rep, "family-tables")
	for k, want := range map[string]string{
		"internal/codec/width.go func width":              "complete names 3/4, MISSING decimal128",
		"encoding/field_type.go const block of FieldType": "complete names 4/4, has decimal128",
		"docs/src/format/field-types.md ## The catalog":   "complete names 4/4, has decimal128",
	} {
		if tables[k] != want {
			t.Errorf("table %s = %q, want %q (tables: %v)", k, tables[k], want, tables)
		}
	}
	if hasKey(tables, "internal/codec/width.go func quiet") || hasKey(tables, "docs/src/format/field-types.md ## Prose") {
		t.Errorf("a comment, or a line naming two types, is no table: %v", tables)
	}
	if len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0], "func width") {
		t.Errorf("warnings = %v, want width's table missing decimal128", rep.Warnings)
	}

	reach := sectionSites(rep, "field-type-reach")
	for _, k := range []string{
		"internal/facet/rule.go func Refusal",      // caller of the selective predicate
		"internal/facet/rule.go func predictFacet", // caller of the refusal (predict)
		"internal/service/facet.go func runFacet",  // caller of the refusal (runtime)
		"internal/engine/cells.go func toFloat64",  // generic reader of the helper
		"internal/engine/post.go func floatValue",  // … in a second file
		"internal/engine/cells.go func decimalCellFloat",
	} {
		if !hasKey(reach, k) {
			t.Errorf("reach lacks %s (got %v)", k, reach)
		}
	}
	if hasKey(reach, "internal/engine/cells.go func decimalSum") {
		t.Errorf("a caller carrying the word is no generic reader: %v", reach)
	}
	formatters := sectionSites(rep, "field-type-formatters")
	if !hasKey(formatters, "internal/engine/group.go func formatKey") || hasKey(formatters, "internal/other/fmt.go func formatX") {
		t.Errorf("formatters = %v, want formatKey (beside an IsDecimal branch) only", formatters)
	}

	// An ordinary word matches only quoted / backticked.
	rep, _ = fieldTypeReport(r, "date", options{})
	if s := findSite(rep, "docs/src/format/field-types.md"); s == nil || !hasSymbol(s, "## Prose") {
		t.Errorf("`date` in backticks is a site: %+v", s)
	}

	// A NEW type joins every complete table.
	rep, _ = fieldTypeReport(r, "bigint", options{})
	tables = sectionSites(rep, "family-tables")
	if tables["internal/codec/width.go func width"] != "complete names 3/4, MISSING bigint" || len(rep.Warnings) != 0 {
		t.Errorf("new type: tables %v, warnings %v (a new member warns nothing)", tables, rep.Warnings)
	}
}

// errorCodeTree: five codes, three tables (one complete, one code-family,
// one near miss).
var errorCodeTree = map[string]string{
	"errors/codes.go": `package errors

type Code string

const (
	PULSE_OVERLAY_A Code = "PULSE_OVERLAY_A"
	PULSE_OVERLAY_B Code = "PULSE_OVERLAY_B"
	PULSE_OVERLAY_C Code = "PULSE_OVERLAY_C"
	PULSE_SLOT_FAILED Code = "PULSE_SLOT_FAILED"
	SERVICE_VALIDATION Code = "SERVICE_VALIDATION"
)
`,
	"errors/fixup_metadata.go": `package errors

var codeMetadata = map[Code]string{
	PULSE_OVERLAY_A: "a", PULSE_OVERLAY_B: "b", PULSE_OVERLAY_C: "c", PULSE_SLOT_FAILED: "s",
}
`,
	"internal/overlay/codes.go": `package overlay

import "github.com/frankbardon/pulse/errors"

var overlayCodes = []errors.Code{errors.PULSE_OVERLAY_A, errors.PULSE_OVERLAY_B, errors.PULSE_OVERLAY_C}

// a function naming three codes is no code-family table
func emit() { _, _, _ = errors.PULSE_OVERLAY_A, errors.PULSE_OVERLAY_B, errors.PULSE_OVERLAY_C }
`,
	"errors/fixup_test.go": "package errors\n\nimport \"testing\"\n\nfunc TestCodesHaveFixups(t *testing.T) { _ = codeMetadata }\n",
}

func TestErrorCodeReport_Rules(t *testing.T) {
	t.Parallel()
	r := fixtureRepo(t, errorCodeTree)
	rep, err := errorCodeReport(r, "SERVICE_VALIDATION", options{})
	if err != nil {
		t.Fatal(err)
	}
	tables := sectionSites(rep, "family-tables")
	if tables["errors/fixup_metadata.go var codeMetadata"] != "complete names 4/5, MISSING SERVICE_VALIDATION" {
		t.Errorf("tables = %v", tables)
	}
	if len(rep.Warnings) != 1 {
		t.Errorf("warnings = %v, want codeMetadata missing it", rep.Warnings)
	}
	if !gateTests(rep)["TestCodesHaveFixups"] {
		t.Errorf("gates lack the codeMetadata reader TestCodesHaveFixups: %+v", rep.Gates)
	}

	rep, _ = errorCodeReport(r, "PULSE_OVERLAY_NEW", options{})
	fam := sectionSites(rep, "code-family-tables")
	if !hasKey(fam, "internal/overlay/codes.go var overlayCodes") || hasKey(fam, "internal/overlay/codes.go func emit") {
		t.Errorf("code-family tables = %v, want overlayCodes only", fam)
	}
	if sectionSites(rep, "family-tables")["errors/fixup_metadata.go var codeMetadata"] == "" {
		t.Error("a new code joins codeMetadata")
	}
	if hasKey(sectionSites(rep, "family-tables"), "internal/overlay/codes.go var overlayCodes") {
		t.Error("an error-code subset is listed only as a code-family table")
	}
	rep, _ = errorCodeReport(r, "PULSE_SLOT_NEW", options{})
	if len(sectionSites(rep, "code-family-tables")) != 0 {
		t.Error("PULSE_SLOT_* has no code-family table")
	}
}

var mcpToolTree = map[string]string{
	"internal/mcp/toolmeta/meta.go": `package toolmeta

const (
	ToolInspect = "pulse_inspect"
	ToolPredict = "pulse_predict"
	ToolProcess = "pulse_process"
	ToolFacet   = "pulse_facet"
)

const (
	DescInspect = "CALL TO SEE A COHORT."
)

func Meta() []string { return []string{ToolInspect, ToolPredict, ToolProcess, ToolFacet} }
`,
	"internal/descriptor/features.go": `package descriptor

import "github.com/frankbardon/pulse/internal/mcp/toolmeta"

var mcpToolBindings = map[string]string{
	toolmeta.ToolInspect: "core", toolmeta.ToolPredict: "core", toolmeta.ToolProcess: "core",
}
`,
	"internal/skills/tool-inspect.md": "# pulse_inspect\n",
	"docs/src/mcp/tools.md":           "# Tools\n\nSee [tool-inspect](../guide/skills/tool-inspect.md).\n",
}

func TestMCPToolReport_Rules(t *testing.T) {
	t.Parallel()
	r := fixtureRepo(t, mcpToolTree)
	rep, err := mcpToolReport(r, "facet", options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Target != "pulse_facet" {
		t.Errorf("target = %s, want the short name resolved", rep.Target)
	}
	tables := sectionSites(rep, "family-tables")
	if tables["internal/descriptor/features.go var mcpToolBindings"] != "complete names 3/4, MISSING pulse_facet" {
		t.Errorf("tables = %v", tables)
	}
	rep, _ = mcpToolReport(r, "pulse_inspect", options{})
	if s := findSite(rep, "docs/src/mcp/tools.md"); s == nil {
		t.Error("the skill stem tool-inspect is a spelling of pulse_inspect")
	}
	if s := findSite(rep, "internal/mcp/toolmeta/meta.go"); !hasSymbol(s, "const DescInspect") {
		t.Errorf("DescInspect is a spelling of pulse_inspect: %+v", s)
	}
	rep, _ = mcpToolReport(r, "frobnicate", options{})
	if rep.Target != "pulse_frobnicate" || !strings.Contains(strings.Join(rep.Summary, "\n"), "internal/skills/tool-frobnicate.md — create") {
		t.Errorf("new tool: target %s, summary %v", rep.Target, rep.Summary)
	}
}

var ioTree = map[string]string{
	"io/format.go": `package io

type Format string

const (
	FormatCSV   Format = "csv"
	FormatTSV   Format = "tsv"
	FormatExcel Format = "excel"
	FormatPulse Format = "pulse"
)

var formats = []Format{FormatCSV, FormatTSV, FormatExcel}
`,
	"io/factory.go": `package io

func NewReader(f Format) string {
	switch f {
	case FormatCSV, FormatTSV:
		return "text"
	}
	return ""
}

func NewWriter(f Format) string {
	switch f {
	case FormatCSV, FormatTSV, FormatExcel:
		return "w"
	}
	return ""
}
`,
	"internal/iocore/iocore.go": `package iocore

type Reader interface {
	ReadHeader() ([]string, error)
	Close() error
}

type Writer interface {
	WriteRow(v []any) error
	Close() error
}

type SchemaAwareReader interface {
	Reader
	PulseSchema() any
}

type NullAwareReader interface {
	NullCells() bool
}
`,
	"internal/io/core.go": `package io

import "github.com/frankbardon/pulse/internal/iocore"

type Reader = iocore.Reader
type Writer = iocore.Writer
type SchemaAwareReader = iocore.SchemaAwareReader
type NullAwareReader = iocore.NullAwareReader
`,
	"internal/io/import.go": `package io

func readSchema(src Reader) {
	if s, ok := src.(SchemaAwareReader); ok {
		_ = s
	}
	if n, ok := src.(NullAwareReader); ok {
		_ = n
	}
}
`,
	"internal/io/csv/csv.go": `package csv

type Reader struct{}

func (r *Reader) ReadHeader() ([]string, error) { return nil, nil }
func (r *Reader) Close() error                  { return nil }

type Writer struct{}

func (w *Writer) WriteRow(v []any) error { return nil }
func (w *Writer) Close() error           { return nil }
`,
	"internal/io/spss/spss.go": `package spss

type Reader struct{}

func (r *Reader) ReadHeader() ([]string, error) { return nil, nil }
func (r *Reader) Close() error                  { return nil }
func (r *Reader) PulseSchema() any              { return nil }
`,
	// near miss: a type with the hook's own method but not its embedded
	// Reader's is no implementer
	"internal/io/meta/meta.go": `package meta

type Schemer struct{}

func (Schemer) PulseSchema() any { return nil }
`,
	"internal/cli/importer.go": `package cli

import _ "github.com/frankbardon/pulse/internal/io/csv"
`,
}

func TestIOReport_Rules(t *testing.T) {
	t.Parallel()
	r := fixtureRepo(t, ioTree)
	rep, err := ioReport(r, "excel", options{})
	if err != nil {
		t.Fatal(err)
	}
	tables := sectionSites(rep, "family-tables")
	if hasKey(tables, "io/factory.go func NewReader") {
		t.Errorf("a switch naming two formats is no table: %v", tables)
	}
	if tables["io/factory.go func NewWriter"] != "complete names 3/3, has excel" {
		t.Errorf("tables = %v", tables)
	}
	// csv: its adapter package and the files importing it
	rep, _ = ioReport(r, "csv", options{})
	adapter := sectionSites(rep, "io-adapter")
	if !hasKey(adapter, "internal/io/csv/csv.go") || !hasKey(adapter, "internal/cli/importer.go") {
		t.Errorf("csv adapter section = %v", adapter)
	}
	// a NEW format: the package to create, and every complete table
	rep, _ = ioReport(r, "feather", options{})
	if !hasKey(sectionSites(rep, "io-adapter"), "internal/io/feather/feather.go") {
		t.Errorf("new format: %v", sectionSites(rep, "io-adapter"))
	}

	// a hook: implementers through embeds, and the pipelines asserting it
	rep, _ = ioReport(r, "SchemaAwareReader", options{})
	impl := sectionSites(rep, "io-implementers")
	if !hasKey(impl, "internal/io/spss/ type Reader") || hasKey(impl, "internal/io/csv/ type Reader") || hasKey(impl, "internal/io/meta/ type Schemer") {
		t.Errorf("implementers = %v, want spss.Reader only (csv lacks PulseSchema, Schemer the embedded Reader)", impl)
	}
	pipes := sectionSites(rep, "io-pipelines")
	if !hasKey(pipes, "internal/io/import.go func readSchema") {
		t.Errorf("pipelines = %v", pipes)
	}
	// a NEW hook: every reader / writer adapter, the pipelines asserting a
	// sibling and the alias block every hook lands in
	rep, _ = ioReport(r, "WidthHintReader", options{})
	impl = sectionSites(rep, "io-implementers")
	if !hasKey(impl, "internal/io/csv/ type Reader") || !hasKey(impl, "internal/io/csv/ type Writer") || !hasKey(impl, "internal/io/spss/ type Reader") {
		t.Errorf("new hook adapters = %v", impl)
	}
	if !hasKey(sectionSites(rep, "io-pipelines"), "internal/io/import.go func readSchema") {
		t.Error("new hook: the pipeline asserting siblings is a site")
	}
	if norm := sectionSites(rep, "family-norm"); !strings.Contains(norm["internal/io/core.go"], "MISSING WidthHintReader") {
		t.Errorf("norm files = %v, want the alias block", norm)
	}
}
