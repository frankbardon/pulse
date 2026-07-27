package pulse_test

import (
	"context"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

func strptr(s string) *string { return &s }

// TestRangeTables_ProgrammaticRegistrationValid asserts a well-formed
// RangeTable registered programmatically survives pulse.New (nothing
// references it — registration is the only contract) and projects into
// the manifest extensions block.
func TestRangeTables_ProgrammaticRegistrationValid(t *testing.T) {
	ext := pulse.Extensions{
		RangeTables: map[string]pulse.RangeTable{
			"quarters": {
				Description: "fiscal quarters",
				Ranges: []pulse.DateRangeSpec{
					{Label: "Q1", Start: strptr("2024-01-01"), End: strptr("2024-03-31")},
					{Label: "Q2", Start: strptr("2024-04-01"), End: strptr("2024-06-30")},
				},
			},
		},
	}
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: ext})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	manifest := p.Manifest(context.Background())
	if len(manifest.Extensions.RangeTables) != 1 {
		t.Fatalf("expected 1 range table in manifest, got %d", len(manifest.Extensions.RangeTables))
	}
	rt := manifest.Extensions.RangeTables[0]
	if rt.Name != "quarters" || rt.Description != "fiscal quarters" || rt.RangeCount != 2 {
		t.Errorf("unexpected range table meta: %+v", rt)
	}
}

// TestRangeTables_ManifestEmptyWhenNoneRegistered asserts the manifest's
// range_tables slice is present and empty (never nil) with no tables.
func TestRangeTables_ManifestEmptyWhenNoneRegistered(t *testing.T) {
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	manifest := p.Manifest(context.Background())
	if manifest.Extensions.RangeTables == nil || len(manifest.Extensions.RangeTables) != 0 {
		t.Errorf("RangeTables = %v; want empty non-nil slice", manifest.Extensions.RangeTables)
	}
}

// TestRangeTables_InvalidRejectedAtNew asserts that a structurally
// invalid table (overlapping ranges) fails pulse.New with the matching
// PULSE_RANGE_* coded error surfaced by the shared compilation pass.
func TestRangeTables_InvalidRejectedAtNew(t *testing.T) {
	cases := []struct {
		name  string
		table pulse.RangeTable
		code  pulseerrors.Code
	}{
		{
			name:  "overlap",
			table: pulse.RangeTable{Ranges: []pulse.DateRangeSpec{{Label: "a", Start: strptr("2024-01-01"), End: strptr("2024-06-30")}, {Label: "b", Start: strptr("2024-03-01"), End: strptr("2024-09-30")}}},
			code:  pulseerrors.PULSE_RANGE_OVERLAP,
		},
		{
			name:  "duplicate_label",
			table: pulse.RangeTable{Ranges: []pulse.DateRangeSpec{{Label: "x", Start: strptr("2024-01-01"), End: strptr("2024-03-31")}, {Label: "x", Start: strptr("2024-04-01"), End: strptr("2024-06-30")}}},
			code:  pulseerrors.PULSE_RANGE_DUPLICATE_LABEL,
		},
		{
			name:  "empty",
			table: pulse.RangeTable{Ranges: nil},
			code:  pulseerrors.PULSE_RANGE_EMPTY,
		},
		{
			name:  "invalid_boundary",
			table: pulse.RangeTable{Ranges: []pulse.DateRangeSpec{{Label: "x", Start: strptr("not-a-date")}}},
			code:  pulseerrors.PULSE_RANGE_INVALID,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pulse.New(pulse.Options{
				FS:         afero.NewMemMapFs(),
				Extensions: pulse.Extensions{RangeTables: map[string]pulse.RangeTable{"t": tc.table}},
			})
			if err == nil {
				t.Fatalf("expected pulse.New to reject invalid range table")
			}
			ce, ok := err.(*pulseerrors.CodedError)
			if !ok {
				t.Fatalf("expected *CodedError, got %T: %v", err, err)
			}
			if ce.Code != tc.code {
				t.Fatalf("code = %s, want %s", ce.Code, tc.code)
			}
			if got, _ := ce.Details["range_table"].(string); got != "t" {
				t.Errorf("expected range_table detail = %q, got %q", "t", got)
			}
		})
	}
}

// TestRangeTables_EmptyNameRejected asserts an empty table name is a
// param-invalid error at New.
func TestRangeTables_EmptyNameRejected(t *testing.T) {
	_, err := pulse.New(pulse.Options{
		FS:         afero.NewMemMapFs(),
		Extensions: pulse.Extensions{RangeTables: map[string]pulse.RangeTable{"": {Ranges: []pulse.DateRangeSpec{{Label: "a"}}}}},
	})
	if err == nil || !strings.Contains(err.Error(), "empty name") {
		t.Fatalf("expected empty-name rejection, got %v", err)
	}
}
