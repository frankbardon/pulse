package processing

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// day is a test helper turning an ISO date literal into the u32 day-integer
// the on-wire `date` field type carries, via the same authority the model
// uses (encoding.ParseDate).
func day(t *testing.T, iso string) uint32 {
	t.Helper()
	d, err := encoding.ParseDate(iso)
	if err != nil {
		t.Fatalf("ParseDate(%q): %v", iso, err)
	}
	return d
}

func ptr(s string) *string { return &s }

// codeOf extracts the error Code from an error, failing the test when the
// error is nil or not a CodedError.
func codeOf(t *testing.T, err error) errors.Code {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	ce, ok := err.(*errors.CodedError)
	if !ok {
		t.Fatalf("expected *errors.CodedError, got %T: %v", err, err)
	}
	return ce.Code
}

func TestCompileDateRanges_Match(t *testing.T) {
	tests := []struct {
		name  string
		specs []DateRangeSpec
		probe string // ISO date to match
		want  string // expected label
		hit   bool
	}{
		{
			name: "inclusive lower edge",
			specs: []DateRangeSpec{
				{Label: "q1", Start: ptr("2024-01-01"), End: ptr("2024-03-31")},
			},
			probe: "2024-01-01", want: "q1", hit: true,
		},
		{
			name: "inclusive upper edge",
			specs: []DateRangeSpec{
				{Label: "q1", Start: ptr("2024-01-01"), End: ptr("2024-03-31")},
			},
			probe: "2024-03-31", want: "q1", hit: true,
		},
		{
			name: "interior day",
			specs: []DateRangeSpec{
				{Label: "q1", Start: ptr("2024-01-01"), End: ptr("2024-03-31")},
			},
			probe: "2024-02-14", want: "q1", hit: true,
		},
		{
			name: "contiguous ranges pick the right bucket (Mar 31)",
			specs: []DateRangeSpec{
				{Label: "q1", Start: ptr("2024-01-01"), End: ptr("2024-03-31")},
				{Label: "q2", Start: ptr("2024-04-01"), End: ptr("2024-06-30")},
			},
			probe: "2024-03-31", want: "q1", hit: true,
		},
		{
			name: "contiguous ranges pick the right bucket (Apr 01)",
			specs: []DateRangeSpec{
				{Label: "q1", Start: ptr("2024-01-01"), End: ptr("2024-03-31")},
				{Label: "q2", Start: ptr("2024-04-01"), End: ptr("2024-06-30")},
			},
			probe: "2024-04-01", want: "q2", hit: true,
		},
		{
			name: "gap yields no match",
			specs: []DateRangeSpec{
				{Label: "q1", Start: ptr("2024-01-01"), End: ptr("2024-03-31")},
				{Label: "q3", Start: ptr("2024-07-01"), End: ptr("2024-09-30")},
			},
			probe: "2024-05-15", want: "", hit: false,
		},
		{
			name: "open lower bound catches early day",
			specs: []DateRangeSpec{
				{Label: "early", End: ptr("2024-03-31")},
				{Label: "later", Start: ptr("2024-04-01")},
			},
			probe: "1999-01-01", want: "early", hit: true,
		},
		{
			name: "open upper bound catches far day",
			specs: []DateRangeSpec{
				{Label: "early", End: ptr("2024-03-31")},
				{Label: "later", Start: ptr("2024-04-01")},
			},
			probe: "2099-12-31", want: "later", hit: true,
		},
		{
			name: "open upper inclusive on its start",
			specs: []DateRangeSpec{
				{Label: "later", Start: ptr("2024-04-01")},
			},
			probe: "2024-04-01", want: "later", hit: true,
		},
		{
			name: "null (nil) boundary is open lower",
			specs: []DateRangeSpec{
				{Label: "everything", Start: nil, End: nil},
			},
			probe: "1000-01-01", want: "everything", hit: true,
		},
		{
			name: "empty-string boundary treated as open",
			specs: []DateRangeSpec{
				{Label: "everything", Start: ptr(""), End: ptr("")},
			},
			probe: "2050-06-06", want: "everything", hit: true,
		},
		{
			name: "day just below open-lower's upper still no match when gap after",
			specs: []DateRangeSpec{
				{Label: "early", End: ptr("2024-03-31")},
			},
			probe: "2024-04-01", want: "", hit: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			set, err := CompileDateRanges(tc.specs)
			if err != nil {
				t.Fatalf("CompileDateRanges: unexpected error: %v", err)
			}
			label, ok := set.Match(day(t, tc.probe))
			if ok != tc.hit {
				t.Fatalf("Match(%s) hit = %v, want %v (label=%q)", tc.probe, ok, tc.hit, label)
			}
			if ok && label != tc.want {
				t.Fatalf("Match(%s) = %q, want %q", tc.probe, label, tc.want)
			}
		})
	}
}

func TestCompileDateRanges_Validation(t *testing.T) {
	tests := []struct {
		name  string
		specs []DateRangeSpec
		want  errors.Code
	}{
		{
			name:  "empty set",
			specs: nil,
			want:  errors.PULSE_RANGE_EMPTY,
		},
		{
			name: "duplicate label",
			specs: []DateRangeSpec{
				{Label: "dup", Start: ptr("2024-01-01"), End: ptr("2024-03-31")},
				{Label: "dup", Start: ptr("2024-04-01"), End: ptr("2024-06-30")},
			},
			want: errors.PULSE_RANGE_DUPLICATE_LABEL,
		},
		{
			name: "start after end",
			specs: []DateRangeSpec{
				{Label: "backwards", Start: ptr("2024-06-30"), End: ptr("2024-01-01")},
			},
			want: errors.PULSE_RANGE_INVALID,
		},
		{
			name: "unparseable start boundary",
			specs: []DateRangeSpec{
				{Label: "bad", Start: ptr("not-a-date"), End: ptr("2024-03-31")},
			},
			want: errors.PULSE_RANGE_INVALID,
		},
		{
			name: "unparseable end boundary",
			specs: []DateRangeSpec{
				{Label: "bad", Start: ptr("2024-01-01"), End: ptr("2024-13-99")},
			},
			want: errors.PULSE_RANGE_INVALID,
		},
		{
			name: "overlapping ranges",
			specs: []DateRangeSpec{
				{Label: "a", Start: ptr("2024-01-01"), End: ptr("2024-04-30")},
				{Label: "b", Start: ptr("2024-04-01"), End: ptr("2024-06-30")},
			},
			want: errors.PULSE_RANGE_OVERLAP,
		},
		{
			name: "overlap on shared inclusive boundary day",
			specs: []DateRangeSpec{
				{Label: "a", Start: ptr("2024-01-01"), End: ptr("2024-03-31")},
				{Label: "b", Start: ptr("2024-03-31"), End: ptr("2024-06-30")},
			},
			want: errors.PULSE_RANGE_OVERLAP,
		},
		{
			name: "open upper overlaps a later bounded range",
			specs: []DateRangeSpec{
				{Label: "tail", Start: ptr("2024-01-01")},
				{Label: "mid", Start: ptr("2024-06-01"), End: ptr("2024-06-30")},
			},
			want: errors.PULSE_RANGE_OVERLAP,
		},
		{
			name: "two open-lower ranges overlap",
			specs: []DateRangeSpec{
				{Label: "a", End: ptr("2024-03-31")},
				{Label: "b", End: ptr("2024-06-30")},
			},
			want: errors.PULSE_RANGE_OVERLAP,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := CompileDateRanges(tc.specs)
			if got := codeOf(t, err); got != tc.want {
				t.Fatalf("CompileDateRanges error code = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCompileDateRanges_ValidDisjointSets(t *testing.T) {
	tests := []struct {
		name  string
		specs []DateRangeSpec
	}{
		{
			name: "contiguous quarters",
			specs: []DateRangeSpec{
				{Label: "q1", Start: ptr("2024-01-01"), End: ptr("2024-03-31")},
				{Label: "q2", Start: ptr("2024-04-01"), End: ptr("2024-06-30")},
			},
		},
		{
			name: "open-lower, bounded middle, open-upper (full cover, disjoint)",
			specs: []DateRangeSpec{
				{Label: "before", End: ptr("2023-12-31")},
				{Label: "y2024", Start: ptr("2024-01-01"), End: ptr("2024-12-31")},
				{Label: "after", Start: ptr("2025-01-01")},
			},
		},
		{
			name: "ranges supplied out of order still validate",
			specs: []DateRangeSpec{
				{Label: "q2", Start: ptr("2024-04-01"), End: ptr("2024-06-30")},
				{Label: "q1", Start: ptr("2024-01-01"), End: ptr("2024-03-31")},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := CompileDateRanges(tc.specs); err != nil {
				t.Fatalf("CompileDateRanges: unexpected error: %v", err)
			}
		})
	}
}
