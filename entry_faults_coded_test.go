package pulse_test

import (
	"context"
	stderrors "errors"
	iofs "io/fs"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	pfs "github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/testutil/fuzzcontract"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
)

// TestEntryFaults_Coded holds every public Predict* / Inspect / Sample /
// Facet / Lookup entry's input validation to the fuzz contract — a nil
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
		if c.code == dataFile && !stderrors.Is(got, iofs.ErrNotExist) {
			t.Errorf("%s: the fs error is not kept as Cause: %v", name, got)
		}
	}
}
