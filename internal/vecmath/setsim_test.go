package vecmath

import (
	"strconv"
	"testing"
)

func TestLabelOverlap(t *testing.T) {
	cases := []struct {
		name string
		a, b []string
		want SetOverlap
	}{
		{"disjoint", []string{"a", "b"}, []string{"c"}, SetOverlap{2, 1, 0}},
		{"partial", []string{"a", "b", "c"}, []string{"b", "c", "d"}, SetOverlap{3, 3, 2}},
		{"duplicates count once", []string{"a", "a", "b"}, []string{"b", "b"}, SetOverlap{2, 1, 1}},
		{"order irrelevant", []string{"c", "a"}, []string{"a", "c"}, SetOverlap{2, 2, 2}},
		{"empty vs empty", nil, []string{}, SetOverlap{0, 0, 0}},
		{"empty vs some", nil, []string{"a"}, SetOverlap{0, 1, 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := append([]string(nil), tc.a...)
			if got := LabelOverlap(tc.a, tc.b); got != tc.want {
				t.Errorf("LabelOverlap(%v, %v) = %+v, want %+v", tc.a, tc.b, got, tc.want)
			}
			for i := range in {
				if in[i] != tc.a[i] {
					t.Error("LabelOverlap mutated its input")
				}
			}
		})
	}
}

func TestSetSimilarityKernels(t *testing.T) {
	cases := []struct {
		o                      SetOverlap
		jaccard, dice, overlap float64
		hamming                int
	}{
		{SetOverlap{3, 3, 2}, 2.0 / 4, 4.0 / 6, 2.0 / 3, 2},
		{SetOverlap{2, 1, 0}, 0, 0, 0, 3},
		{SetOverlap{2, 2, 2}, 1, 1, 1, 0},
		{SetOverlap{4, 1, 1}, 1.0 / 4, 2.0 / 5, 1, 3}, // subset: overlap 1
		{SetOverlap{0, 0, 0}, nan, nan, nan, 0},       // empty vs empty
		{SetOverlap{0, 3, 0}, 0, 0, nan, 3},           // one empty: overlap undefined
		{SetOverlap{256, 256, 256}, 1, 1, 1, 0},
		{SetOverlap{256, 1, 1}, 1.0 / 256, 2.0 / 257, 1, 255},
	}
	for i, tc := range cases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			if got := Jaccard(tc.o); !near(got, tc.jaccard) {
				t.Errorf("Jaccard(%+v) = %v, want %v", tc.o, got, tc.jaccard)
			}
			if got := Dice(tc.o); !near(got, tc.dice) {
				t.Errorf("Dice(%+v) = %v, want %v", tc.o, got, tc.dice)
			}
			if got := Overlap(tc.o); !near(got, tc.overlap) {
				t.Errorf("Overlap(%+v) = %v, want %v", tc.o, got, tc.overlap)
			}
			if got := Hamming(tc.o); got != tc.hamming {
				t.Errorf("Hamming(%+v) = %v, want %v", tc.o, got, tc.hamming)
			}
		})
	}
}
