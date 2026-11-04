package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	iofs "io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	pfs "github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/testutil/fuzzcontract"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
)

// ftf is a FilterToFileWithRequest request over a missing source with
// the given filterers and no expression.
func ftf(filts ...*types.Filterer) *pulse.FilterToFileRequest {
	return &pulse.FilterToFileRequest{SourcePath: "missing.pulse", OutputDir: "out", Filterers: filts}
}

// TestEntryFaults_Coded holds every public Predict* / Inspect / Sample /
// Facet / Lookup / Process / stream / synth / I/O-job / FilterToFile
// entry's input validation to the fuzz contract — a nil
// request, a request without a cohort, and a cohort that does not exist
// each return a registered *errors.CodedError, never an uncoded fmt
// error and never a panic — and pins the code: a missing input is
// SERVICE_VALIDATION; an unopenable cohort on a no-execute surface is
// DATA_FILE (the predict roots' code) with the fs error kept as Cause.
// The executing Sample / Facet / Lookup arms keep the runtime open's
// SERVICE_RESOURCE.
func TestEntryFaults_Coded(t *testing.T) {
	t.Parallel()
	p, err := pulse.New(pulse.Options{FS: pfs.NewMemMap().Fs()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	missing := &types.Cohort{Filename: "missing.pulse"}
	anchor := &types.Cohort{Filename: "missing.pulse#a.pulse"}
	discard := func(_ any, err error) error { return err }
	const (
		validation = perr.SERVICE_VALIDATION
		dataFile   = perr.DATA_FILE
		resource   = perr.SERVICE_RESOURCE
	)
	cases := map[string]struct {
		call func() error
		code perr.Code
	}{
		"Predict nil":                 {func() error { return discard(p.Predict(ctx, nil)) }, validation},
		"Predict no cohort":           {func() error { return discard(p.Predict(ctx, &types.Request{})) }, validation},
		"Predict missing":             {func() error { return discard(p.Predict(ctx, &types.Request{Cohort: missing})) }, dataFile},
		"Predict anchor":              {func() error { return discard(p.Predict(ctx, &types.Request{Cohort: anchor})) }, dataFile},
		"PredictEnvelope nil":         {func() error { return discard(p.PredictEnvelope(ctx, nil)) }, validation},
		"PredictEnvelope no cohort":   {func() error { return discard(p.PredictEnvelope(ctx, &types.Request{})) }, validation},
		"PredictEnvelope missing":     {func() error { return discard(p.PredictEnvelope(ctx, &types.Request{Cohort: missing})) }, dataFile},
		"PredictBytes nil":            {func() error { return discard(p.PredictBytes(ctx, nil, nil)) }, validation},
		"PredictFacet nil":            {func() error { return discard(p.PredictFacet(ctx, nil)) }, validation},
		"PredictFacet no cohort":      {func() error { return discard(p.PredictFacet(ctx, &types.FacetRequest{})) }, validation},
		"PredictFacet missing":        {func() error { return discard(p.PredictFacet(ctx, &types.FacetRequest{Cohort: missing})) }, dataFile},
		"PredictChain nil":            {func() error { return discard(p.PredictChain(ctx, nil)) }, validation},
		"PredictChain missing":        {func() error { return discard(p.PredictChain(ctx, &types.ChainRequest{Cohort: missing})) }, dataFile},
		"PredictMaterialize nil":      {func() error { return discard(p.PredictMaterialize(ctx, nil)) }, validation},
		"PredictMaterialize missing":  {func() error { return discard(p.PredictMaterialize(ctx, &types.Request{Cohort: missing})) }, dataFile},
		"PredictExport nil":           {func() error { return discard(p.PredictExport(ctx, nil)) }, validation},
		"PredictExport missing":       {func() error { return discard(p.PredictExport(ctx, &pio.ExportJob{Source: "missing.pulse"})) }, dataFile},
		"Inspect missing":             {func() error { return discard(p.Inspect(ctx, "missing.pulse")) }, dataFile},
		"Inspect anchor":              {func() error { return discard(p.Inspect(ctx, "missing.pulse#a.pulse")) }, dataFile},
		"InspectEnvelope missing":     {func() error { return discard(p.InspectEnvelope(ctx, "missing.pulse", nil)) }, dataFile},
		"Sample missing":              {func() error { return discard(p.Sample(ctx, "missing.pulse", 3)) }, resource},
		"SampleWithRequest nil":       {func() error { return discard(p.SampleWithRequest(ctx, nil)) }, validation},
		"SampleWithRequest no cohort": {func() error { return discard(p.SampleWithRequest(ctx, &types.SampleRequest{})) }, validation},
		"SampleWithRequest missing": {func() error {
			return discard(p.SampleWithRequest(ctx, &types.SampleRequest{Cohort: missing, N: 3}))
		}, resource},
		"Facet missing":         {func() error { return discard(p.Facet(ctx, "missing.pulse", "x")) }, resource},
		"FacetSchema nil":       {func() error { return discard(p.FacetSchema(ctx, nil)) }, validation},
		"FacetSchema no cohort": {func() error { return discard(p.FacetSchema(ctx, &types.FacetRequest{})) }, validation},
		"FacetSchema missing": {func() error {
			return discard(p.FacetSchema(ctx, &types.FacetRequest{Cohort: missing, Fields: []string{"x"}}))
		}, resource},
		"Lookup nil":       {func() error { return discard(p.Lookup(ctx, nil)) }, validation},
		"Lookup no cohort": {func() error { return discard(p.Lookup(ctx, &types.LookupRequest{})) }, validation},
		"Lookup missing": {func() error {
			return discard(p.Lookup(ctx, &types.LookupRequest{Cohort: missing, Field: "x"}))
		}, resource},
		"Process nil":             {func() error { return discard(p.Process(ctx, nil)) }, validation},
		"ProcessStreamResult nil": {func() error { return discard(p.ProcessStreamResult(ctx, nil)) }, validation},
		"SynthStream nil":         {func() error { return discard(p.SynthStream(ctx, nil, pulse.SynthOptions{})) }, validation},
		"Synth nil":               {func() error { return discard(p.Synth(ctx, nil, "out.pulse", pulse.SynthOptions{})) }, validation},
		"Import nil":              {func() error { return discard(p.Import(ctx, nil)) }, validation},
		"Export nil":              {func() error { return discard(p.Export(ctx, nil)) }, validation},
		"ExportTransfer nil":      {func() error { return discard(p.ExportTransfer(ctx, nil)) }, validation},
		"ImportTransfer nil":      {func() error { return discard(p.ImportTransfer(ctx, nil)) }, validation},
		"Convert nil":             {func() error { return discard(p.Convert(ctx, nil)) }, validation},
		"FilterToFile nil":        {func() error { return discard(p.FilterToFileWithRequest(ctx, nil)) }, validation},
		"FilterToFile no source": {func() error {
			return discard(p.FilterToFileWithRequest(ctx, &pulse.FilterToFileRequest{OutputDir: "out", Expression: "x > 1"}))
		}, validation},
		"FilterToFile no output dir": {func() error {
			return discard(p.FilterToFileWithRequest(ctx, &pulse.FilterToFileRequest{SourcePath: "missing.pulse", Expression: "x > 1"}))
		}, validation},
		"FilterToFile no predicate": {func() error {
			return discard(p.FilterToFileWithRequest(ctx, ftf()))
		}, validation},
		"FilterToFile both predicates": {func() error {
			r := ftf(&types.Filterer{Type: types.FILTER_EXPRESSION, Expression: "x > 1"})
			r.Expression = "x > 1"
			return discard(p.FilterToFileWithRequest(ctx, r))
		}, validation},
		"FilterToFile nil filterer": {func() error { return discard(p.FilterToFileWithRequest(ctx, ftf(nil))) }, validation},
		"FilterToFile empty expression filterer": {func() error {
			return discard(p.FilterToFileWithRequest(ctx, ftf(&types.Filterer{Type: types.FILTER_EXPRESSION})))
		}, validation},
		"FilterToFile range arity": {func() error {
			return discard(p.FilterToFileWithRequest(ctx, ftf(&types.Filterer{Type: types.FILTER_RANGE, Field: "x", Values: []string{"1"}})))
		}, validation},
		"FilterToFile null mode": {func() error {
			return discard(p.FilterToFileWithRequest(ctx, ftf(&types.Filterer{Type: types.FILTER_NULL, Field: "x", Values: []string{"sometimes"}})))
		}, validation},
		"FilterToFile missing source": {func() error {
			r := ftf()
			r.Expression = "x > 1"
			return discard(p.FilterToFileWithRequest(ctx, r))
		}, resource},
	}
	for name, c := range cases {
		var got error
		fuzzcontract.Run(t, []byte(name), func([]byte) error {
			got = c.call()
			return got
		})
		var ce *perr.CodedError
		if !stderrors.As(got, &ce) || ce.Code != c.code {
			t.Errorf("%s: want %s, got %v", name, c.code, got)
			continue
		}
		if (c.code == dataFile || name == "FilterToFile missing source") && !stderrors.Is(got, iofs.ErrNotExist) {
			t.Errorf("%s: the fs error is not kept as Cause: %v", name, got)
		}
	}
}

// TestNewFaults_Coded holds pulse.New's argument and table-directory
// faults to the fuzz contract: a negative worker knob is
// SERVICE_VALIDATION; a label / range / lookup tables dir or a
// populations dir that is not a directory, and a label or range table
// file that does not parse, is DATA_FILE naming the path
// (details.path) and the loader (details.kind), the parse error kept as
// Cause. A lookup table file keeps its own PULSE_LOOKUP_* code.
func TestNewFaults_Coded(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "plain.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "bad")
	if err := os.Mkdir(bad, 0o700); err != nil {
		t.Fatal(err)
	}
	badTable := filepath.Join(bad, "t.json")
	if err := os.WriteFile(badTable, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	mem := pfs.NewMemMap().Fs()
	cases := map[string]struct {
		opts       pulse.Options
		code       perr.Code
		path, kind string
	}{
		"ShardWorkers negative":  {opts: pulse.Options{ShardWorkers: -1}, code: perr.SERVICE_VALIDATION},
		"DecodeWorkers negative": {opts: pulse.Options{DecodeWorkers: -1}, code: perr.SERVICE_VALIDATION},
		"label dir is a file":    {opts: pulse.Options{LabelTablesDir: file}, code: perr.DATA_FILE, path: file, kind: "label_table"},
		"range dir is a file":    {opts: pulse.Options{RangeTablesDir: file}, code: perr.DATA_FILE, path: file, kind: "range_table"},
		"lookup dir is a file":   {opts: pulse.Options{LookupTablesDir: file}, code: perr.DATA_FILE, path: file, kind: "lookup_table"},
		"populations dir is a file": {
			opts: pulse.Options{PopulationsDir: file}, code: perr.DATA_FILE, path: file, kind: "population",
		},
		"label table unparseable":  {opts: pulse.Options{LabelTablesDir: bad}, code: perr.DATA_FILE, path: badTable, kind: "label_table"},
		"range table unparseable":  {opts: pulse.Options{RangeTablesDir: bad}, code: perr.DATA_FILE, path: badTable, kind: "range_table"},
		"lookup table unparseable": {opts: pulse.Options{LookupTablesDir: bad}, code: perr.PULSE_LOOKUP_FILE_INVALID, path: badTable},
	}
	for name, c := range cases {
		c.opts.FS = mem
		var got error
		fuzzcontract.Run(t, []byte(name), func([]byte) error {
			_, got = pulse.New(c.opts)
			return got
		})
		var ce *perr.CodedError
		if !stderrors.As(got, &ce) || ce.Code != c.code {
			t.Errorf("%s: want %s, got %v", name, c.code, got)
			continue
		}
		if c.path != "" && ce.Details["path"] != c.path {
			t.Errorf("%s: details.path = %v, want %s", name, ce.Details["path"], c.path)
		}
		if c.kind != "" && ce.Details["kind"] != c.kind {
			t.Errorf("%s: details.kind = %v, want %s", name, ce.Details["kind"], c.kind)
		}
		var syn *json.SyntaxError
		if strings.HasSuffix(name, "unparseable") && c.code == perr.DATA_FILE && !stderrors.As(got, &syn) {
			t.Errorf("%s: the parse error is not kept as Cause: %v", name, got)
		}
	}
}

// TestNewFaults_NoDataDirCoded: with no FS, no DataDir and
// PULSE_DATA_DIR unset, New's filesystem fault is SERVICE_VALIDATION,
// not an uncoded error.
//
// serial: t.Setenv clears PULSE_DATA_DIR for the process.
func TestNewFaults_NoDataDirCoded(t *testing.T) {
	t.Setenv("PULSE_DATA_DIR", "")
	_, err := pulse.New(pulse.Options{})
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.SERVICE_VALIDATION {
		t.Fatalf("want SERVICE_VALIDATION, got %v", err)
	}
}
