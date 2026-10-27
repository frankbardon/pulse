package metric

import (
	"slices"
	"testing"
)

// TestDefault_KindTable pins the kind → default table (PRD FR-10):
// every default answers in its form, suits its kind and is never a
// poor fit for it.
func TestDefault_KindTable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		k          Kind
		f          Form
		want       string
		hasDefault bool
	}{
		{Measure, Distance, "std_euclidean", true},
		{Measure, Similarity, "", false},
		{Scale, Distance, "euclidean", true},
		{Scale, Similarity, "correlation", true},
		{Composition, Distance, "aitchison", true},
		{Composition, Similarity, "cosine", true},
		{Binary, Distance, "hamming", true},
		{Binary, Similarity, "jaccard", true},
		{Kind("ordinal"), Distance, "", false},
	}
	for _, c := range cases {
		m, ok := Default(c.k, c.f)
		if ok != c.hasDefault || m.Name() != c.want {
			t.Errorf("Default(%s, %s) = %q, %v; want %q, %v", c.k, c.f, m.Name(), ok, c.want, c.hasDefault)
			continue
		}
		if !ok {
			continue
		}
		if !m.HasForm(c.f) || !slices.Contains(m.KindsSuited(), c.k) || m.PoorFitFor(c.k) {
			t.Errorf("Default(%s, %s) = %s: forms %v, suited %v, poor %v", c.k, c.f, m.Name(), m.Forms(), m.KindsSuited(), m.PoorFit())
		}
	}
}

// TestDefaultFor_ConsumerFallback: a consumer that accepts the kind's
// default gets it; one that does not gets the first accepted metric
// suiting the kind (never a poor fit); admit narrows the choice; none
// qualifying is false.
func TestDefaultFor_ConsumerFallback(t *testing.T) {
	t.Parallel()

	nonNeg := func(m Metric) bool { return m.Domain() != DomainNonNegative }
	cases := []struct {
		c     Consumer
		k     Kind
		f     Form
		admit func(Metric) bool
		want  string
	}{
		{ConsumerVecDistance, Measure, Distance, nil, "std_euclidean"},
		{ConsumerVecDistance, Binary, Distance, nil, "hamming"},
		{ConsumerMatDistance, Measure, Distance, nil, "euclidean"},
		{ConsumerMatDistance, Scale, Distance, nil, "euclidean"},
		{ConsumerMatDistance, Composition, Distance, nil, "aitchison"},
		{ConsumerMatDistance, Composition, Distance, nonNeg, "manhattan"},
		{ConsumerMatDistance, Binary, Distance, nil, "cosine"},
		{ConsumerProfileSimilarity, Scale, Similarity, nil, "correlation"},
		{ConsumerProfileSimilarity, Measure, Similarity, nil, ""},
		{ConsumerKMeans, Composition, Distance, nil, ""},
		{ConsumerKMeans, Scale, Distance, nil, "euclidean"},
	}
	for _, c := range cases {
		m, ok := DefaultFor(c.c, c.k, c.f, c.admit)
		if m.Name() != c.want || ok != (c.want != "") {
			t.Errorf("DefaultFor(%s, %s, %s) = %q, %v; want %q", c.c, c.k, c.f, m.Name(), ok, c.want)
			continue
		}
		if ok && (m.PoorFitFor(c.k) || !Accepts(c.c, m.Name())) {
			t.Errorf("DefaultFor(%s, %s) = %s is a poor fit or unaccepted", c.c, c.k, m.Name())
		}
	}
}
