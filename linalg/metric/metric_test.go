package metric_test

import (
	stderrors "errors"
	"slices"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg/metric"
)

// TestRegistryListsExactlyTheV1Metrics pins the registry to the 15 v1
// metrics in canonical order.
func TestRegistryListsExactlyTheV1Metrics(t *testing.T) {
	want := []string{
		"euclidean", "sq_euclidean", "manhattan", "chebyshev", "cosine", "correlation", "hellinger", "aitchison",
		"jaccard", "dice", "hamming", "overlap",
		"std_euclidean", "mahalanobis", "chi_square",
	}
	if got := metric.Names(); !slices.Equal(got, want) {
		t.Fatalf("Names() = %v\nwant %v", got, want)
	}
	all := metric.All()
	if len(all) != len(want) {
		t.Fatalf("All() has %d metrics, want %d", len(all), len(want))
	}
	for i, m := range all {
		if m.Name() != want[i] || !m.Valid() {
			t.Errorf("All()[%d] = %q (valid %v), want %q", i, m.Name(), m.Valid(), want[i])
		}
		if got, ok := metric.Lookup(want[i]); !ok || got.Name() != want[i] {
			t.Errorf("Lookup(%q) = %q, %v", want[i], got.Name(), ok)
		}
		if m.Description() == "" {
			t.Errorf("%s has no description", m.Name())
		}
	}
	if _, ok := metric.Lookup("sqeuclidean"); ok {
		t.Error("Lookup(sqeuclidean) found a metric; the v1 spelling is sq_euclidean with no alias")
	}
}

type decl struct {
	family    metric.Family
	direction metric.Form
	forms     []metric.Form
	suited    []metric.Kind
	poor      []metric.Kind
	fit       metric.Fit
	domain    metric.Domain
	scaled    bool
	complete  bool
	zeroUndef bool
	consumers []metric.Consumer
}

var (
	sim  = metric.Similarity
	dist = metric.Distance
	cE   = metric.ConsumerExpr
	cV   = metric.ConsumerVecDistance
	cP   = metric.ConsumerProfileSimilarity
	cM   = metric.ConsumerMatDistance
	cK   = metric.ConsumerKMeans
	kM   = metric.Measure
	kS   = metric.Scale
	kC   = metric.Composition
	kB   = metric.Binary
)

// declarations is every metric's expected declaration.
var declarations = map[string]decl{
	"euclidean":     {metric.FamilyPlain, dist, []metric.Form{dist}, []metric.Kind{kM, kS}, []metric.Kind{kC}, metric.FitNone, metric.DomainReal, true, false, false, []metric.Consumer{cE, cV, cM, cK}},
	"sq_euclidean":  {metric.FamilyPlain, dist, []metric.Form{dist}, []metric.Kind{kM, kS}, []metric.Kind{kC}, metric.FitNone, metric.DomainReal, true, false, false, []metric.Consumer{cE, cV, cM, cK}},
	"manhattan":     {metric.FamilyPlain, dist, []metric.Form{dist}, []metric.Kind{kM, kC}, []metric.Kind{}, metric.FitNone, metric.DomainReal, true, false, false, []metric.Consumer{cE, cV, cM}},
	"chebyshev":     {metric.FamilyPlain, dist, []metric.Form{dist}, []metric.Kind{kM}, []metric.Kind{}, metric.FitNone, metric.DomainReal, false, false, false, []metric.Consumer{cE, cV, cM}},
	"cosine":        {metric.FamilyPlain, sim, []metric.Form{sim, dist}, []metric.Kind{kC, kB}, []metric.Kind{kM, kS}, metric.FitNone, metric.DomainReal, false, false, false, []metric.Consumer{cE, cV, cP, cM}},
	"correlation":   {metric.FamilyPlain, sim, []metric.Form{sim}, []metric.Kind{kS}, []metric.Kind{}, metric.FitNone, metric.DomainReal, false, false, false, []metric.Consumer{cE, cP}},
	"hellinger":     {metric.FamilyPlain, dist, []metric.Form{dist}, []metric.Kind{kC}, []metric.Kind{}, metric.FitNone, metric.DomainNonNegative, false, false, false, []metric.Consumer{cE, cV, cM}},
	"aitchison":     {metric.FamilyPlain, dist, []metric.Form{dist}, []metric.Kind{kC}, []metric.Kind{}, metric.FitNone, metric.DomainNonNegative, false, false, true, []metric.Consumer{cE, cV, cM}},
	"jaccard":       {metric.FamilySet, sim, []metric.Form{sim}, []metric.Kind{kB}, []metric.Kind{}, metric.FitNone, metric.DomainBinary, false, false, false, []metric.Consumer{cE, cP}},
	"dice":          {metric.FamilySet, sim, []metric.Form{sim}, []metric.Kind{kB}, []metric.Kind{}, metric.FitNone, metric.DomainBinary, false, false, false, []metric.Consumer{cE, cP}},
	"hamming":       {metric.FamilySet, dist, []metric.Form{dist}, []metric.Kind{kB}, []metric.Kind{}, metric.FitNone, metric.DomainBinary, true, false, false, []metric.Consumer{cE, cV}},
	"overlap":       {metric.FamilySet, sim, []metric.Form{sim}, []metric.Kind{kB}, []metric.Kind{}, metric.FitNone, metric.DomainBinary, false, false, false, []metric.Consumer{cE, cP}},
	"std_euclidean": {metric.FamilyFitted, dist, []metric.Form{dist}, []metric.Kind{kM}, []metric.Kind{kC}, metric.FitSD, metric.DomainReal, true, false, false, []metric.Consumer{cV}},
	"mahalanobis":   {metric.FamilyFitted, dist, []metric.Form{dist}, []metric.Kind{kM}, []metric.Kind{}, metric.FitInverseCovariance, metric.DomainReal, false, true, false, []metric.Consumer{cV, cM}},
	"chi_square":    {metric.FamilyFitted, dist, []metric.Form{dist}, []metric.Kind{kC}, []metric.Kind{}, metric.FitColumnMasses, metric.DomainNonNegative, false, false, false, []metric.Consumer{}},
}

// TestEveryMetricDeclaresItsContract checks each metric's kinds, poor
// fits, direction, forms, fitted flag, domain rules and consumers.
func TestEveryMetricDeclaresItsContract(t *testing.T) {
	if len(declarations) != len(metric.Names()) {
		t.Fatalf("declarations cover %d metrics, registry has %d", len(declarations), len(metric.Names()))
	}
	for _, m := range metric.All() {
		want, ok := declarations[m.Name()]
		if !ok {
			t.Errorf("%s: no expected declaration", m.Name())
			continue
		}
		check := func(what string, got, exp any, eq bool) {
			t.Helper()
			if !eq {
				t.Errorf("%s.%s = %v, want %v", m.Name(), what, got, exp)
			}
		}
		check("Family", m.Family(), want.family, m.Family() == want.family)
		check("Direction", m.Direction(), want.direction, m.Direction() == want.direction)
		check("Forms", m.Forms(), want.forms, slices.Equal(m.Forms(), want.forms))
		check("KindsSuited", m.KindsSuited(), want.suited, slices.Equal(m.KindsSuited(), want.suited))
		check("PoorFit", m.PoorFit(), want.poor, slices.Equal(m.PoorFit(), want.poor))
		check("Fit", m.Fit(), want.fit, m.Fit() == want.fit)
		check("Fitted", m.Fitted(), want.fit != metric.FitNone, m.Fitted() == (want.fit != metric.FitNone))
		check("Domain", m.Domain(), want.domain, m.Domain() == want.domain)
		check("Scaled", m.Scaled(), want.scaled, m.Scaled() == want.scaled)
		check("Complete", m.Complete(), want.complete, m.Complete() == want.complete)
		check("ZeroUndefined", m.ZeroUndefined(), want.zeroUndef, m.ZeroUndefined() == want.zeroUndef)
		check("Consumers", m.Consumers(), want.consumers, slices.Equal(m.Consumers(), want.consumers))
		if m.Forms()[0] != m.Direction() {
			t.Errorf("%s: Forms() must lead with the native direction", m.Name())
		}
		for _, f := range []metric.Form{sim, dist} {
			if m.HasForm(f) != slices.Contains(want.forms, f) {
				t.Errorf("%s.HasForm(%s) = %v", m.Name(), f, m.HasForm(f))
			}
		}
		for _, k := range metric.Kinds() {
			if m.PoorFitFor(k) != slices.Contains(want.poor, k) {
				t.Errorf("%s.PoorFitFor(%s) = %v", m.Name(), k, m.PoorFitFor(k))
			}
			if slices.Contains(want.poor, k) && slices.Contains(want.suited, k) {
				t.Errorf("%s: kind %s is both suited and a poor fit", m.Name(), k)
			}
		}
	}
}

// TestDeclaredSlicesAreCopies proves a caller mutating a returned slice
// cannot reach the registry.
func TestDeclaredSlicesAreCopies(t *testing.T) {
	m, _ := metric.Lookup("cosine")
	m.Forms()[0] = metric.Distance
	m.KindsSuited()[0] = metric.Measure
	m.PoorFit()[0] = metric.Binary
	m.Consumers()[0] = metric.ConsumerKMeans
	metric.ConsumerExpr.Forms()[0] = metric.Distance
	again, _ := metric.Lookup("cosine")
	if again.Forms()[0] != metric.Similarity || again.KindsSuited()[0] != metric.Composition ||
		again.PoorFit()[0] != metric.Measure || again.Consumers()[0] != metric.ConsumerExpr ||
		metric.ConsumerExpr.Forms()[0] != metric.Similarity {
		t.Fatal("a returned slice aliases the registry")
	}
}

// TestConsumerAcceptanceRules pins the per-consumer acceptance rules
// the lists must obey, independently of the per-metric declarations.
func TestConsumerAcceptanceRules(t *testing.T) {
	euclidFamily := []string{"euclidean", "sq_euclidean"}
	for _, m := range metric.All() {
		for _, c := range m.Consumers() {
			if !metric.Accepts(c, m.Name()) {
				t.Errorf("Accepts(%s, %s) = false for a declared consumer", c, m.Name())
			}
			// Every accepted metric answers in a form the consumer asks for.
			if !slices.ContainsFunc(c.Forms(), m.HasForm) {
				t.Errorf("%s accepts %s, which has none of its forms %v", c, m.Name(), c.Forms())
			}
			switch c {
			case metric.ConsumerExpr:
				if m.Fitted() {
					t.Errorf("expr accepts fitted metric %s", m.Name())
				}
			case metric.ConsumerVecDistance:
				if m.Fitted() && m.Fit() == metric.FitColumnMasses {
					t.Errorf("ATTR_VEC_DISTANCE accepts %s", m.Name())
				}
			case metric.ConsumerProfileSimilarity:
				if !m.HasForm(metric.Similarity) || m.Fitted() {
					t.Errorf("ATTR_PROFILE_SIMILARITY accepts %s", m.Name())
				}
			case metric.ConsumerKMeans:
				if !slices.Contains(euclidFamily, m.Name()) {
					t.Errorf("GROUP_KMEANS accepts non-Euclidean %s", m.Name())
				}
			}
		}
	}
	// Every row-local metric is reachable from the expression layer, and
	// every row-local distance-form metric from ATTR_VEC_DISTANCE.
	for _, m := range metric.All() {
		if !m.Fitted() && !metric.Accepts(metric.ConsumerExpr, m.Name()) {
			t.Errorf("expr does not accept row-local %s", m.Name())
		}
		if !m.Fitted() && m.HasForm(metric.Distance) && !metric.Accepts(metric.ConsumerVecDistance, m.Name()) {
			t.Errorf("ATTR_VEC_DISTANCE does not accept row-local distance %s", m.Name())
		}
		if !m.Fitted() && m.HasForm(metric.Similarity) && !metric.Accepts(metric.ConsumerProfileSimilarity, m.Name()) {
			t.Errorf("ATTR_PROFILE_SIMILARITY does not accept row-local similarity %s", m.Name())
		}
	}
	if got := metric.Accepted(metric.ConsumerKMeans); !slices.Equal(got, euclidFamily) {
		t.Errorf("Accepted(GROUP_KMEANS) = %v, want %v", got, euclidFamily)
	}
	if got, want := metric.Accepted(metric.ConsumerMatDistance), []string{"euclidean", "sq_euclidean", "manhattan", "chebyshev", "cosine", "hellinger", "aitchison", "mahalanobis"}; !slices.Equal(got, want) {
		t.Errorf("Accepted(MAT_DISTANCE) = %v, want %v", got, want)
	}
	if got, want := metric.Accepted(metric.ConsumerVecDistance), []string{"euclidean", "sq_euclidean", "manhattan", "chebyshev", "cosine", "hellinger", "aitchison", "hamming", "std_euclidean", "mahalanobis"}; !slices.Equal(got, want) {
		t.Errorf("Accepted(ATTR_VEC_DISTANCE) = %v, want %v", got, want)
	}
	// chi_square is registered but unexposed.
	for _, c := range metric.Consumers() {
		if metric.Accepts(c, "chi_square") {
			t.Errorf("%s accepts chi_square; it is registered but unexposed", c)
		}
	}
	if metric.Accepts(metric.ConsumerExpr, "nope") {
		t.Error("Accepts an unregistered name")
	}
	if got := metric.Accepted("NOT_A_CONSUMER"); len(got) != 0 {
		t.Errorf("Accepted(unknown consumer) = %v", got)
	}
	if got := metric.Consumer("NOT_A_CONSUMER").Forms(); got != nil {
		t.Errorf("unknown consumer Forms() = %v", got)
	}
	if got, want := metric.Consumers(), []metric.Consumer{cE, cV, cP, cM, cK}; !slices.Equal(got, want) {
		t.Errorf("Consumers() = %v", got)
	}
	if got, want := metric.Kinds(), []metric.Kind{kM, kS, kC, kB}; !slices.Equal(got, want) {
		t.Errorf("Kinds() = %v", got)
	}
}

// codeOf unwraps err to its coded error.
func codeOf(t *testing.T, err error) *perr.CodedError {
	t.Helper()
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("error %v (%T) is not a *errors.CodedError", err, err)
	}
	return ce
}

// TestResolve_DirectionContract: a consumer asks for a form; a metric
// with no such form is refused, never transformed.
func TestResolve_DirectionContract(t *testing.T) {
	cases := []struct {
		name   string
		form   metric.Form
		code   perr.Code
		reason string
	}{
		{"euclidean", metric.Distance, "", ""},
		{"cosine", metric.Similarity, "", ""},
		{"cosine", metric.Distance, "", ""},
		{"correlation", metric.Similarity, "", ""},
		{"euclidean", metric.Similarity, perr.PULSE_VECTOR_METRIC_UNKNOWN, "no_form"},
		{"correlation", metric.Distance, perr.PULSE_VECTOR_METRIC_UNKNOWN, "no_form"},
		{"jaccard", metric.Distance, perr.PULSE_VECTOR_METRIC_UNKNOWN, "no_form"},
		{"hamming", metric.Similarity, perr.PULSE_VECTOR_METRIC_UNKNOWN, "no_form"},
		{"chi_square", metric.Similarity, perr.PULSE_VECTOR_METRIC_UNKNOWN, "no_form"},
		{"sqeuclidean", metric.Distance, perr.PULSE_VECTOR_METRIC_UNKNOWN, "unregistered"},
		{"", metric.Distance, perr.PULSE_VECTOR_METRIC_REQUIRED, ""},
	}
	for _, tc := range cases {
		m, err := metric.Resolve(tc.name, tc.form)
		if tc.code == "" {
			if err != nil || m.Name() != tc.name {
				t.Errorf("Resolve(%q, %s) = %q, %v", tc.name, tc.form, m.Name(), err)
			}
			continue
		}
		if m.Valid() {
			t.Errorf("Resolve(%q, %s) returned a valid metric beside a refusal", tc.name, tc.form)
		}
		ce := codeOf(t, err)
		if ce.Code != tc.code {
			t.Errorf("Resolve(%q, %s) code %s, want %s", tc.name, tc.form, ce.Code, tc.code)
		}
		if tc.reason != "" && ce.Details["reason"] != tc.reason {
			t.Errorf("Resolve(%q, %s) reason %v, want %s", tc.name, tc.form, ce.Details["reason"], tc.reason)
		}
		if ce.Details["form"] != string(tc.form) {
			t.Errorf("Resolve(%q, %s) form detail %v", tc.name, tc.form, ce.Details["form"])
		}
		valid, _ := ce.Details["valid"].([]string)
		for _, n := range valid {
			if mm, _ := metric.Lookup(n); !mm.HasForm(tc.form) {
				t.Errorf("Resolve(%q, %s) lists %s as valid, which has no %s form", tc.name, tc.form, n, tc.form)
			}
		}
		if len(valid) == 0 {
			t.Errorf("Resolve(%q, %s) lists no valid metric", tc.name, tc.form)
		}
	}
}

// TestResolveFor_PerConsumer: each consumer gets only its accepted
// metrics in its own forms.
func TestResolveFor_PerConsumer(t *testing.T) {
	cases := []struct {
		c      metric.Consumer
		name   string
		form   metric.Form
		code   perr.Code
		reason string
	}{
		{cK, "euclidean", dist, "", ""},
		{cK, "manhattan", dist, perr.PULSE_VECTOR_METRIC_UNKNOWN, "not_accepted"},
		{cK, "cosine", dist, perr.PULSE_VECTOR_METRIC_UNKNOWN, "not_accepted"},
		{cP, "correlation", sim, "", ""},
		{cP, "cosine", sim, "", ""},
		{cP, "euclidean", sim, perr.PULSE_VECTOR_METRIC_UNKNOWN, "no_form"},
		{cP, "cosine", dist, perr.PULSE_VECTOR_METRIC_UNKNOWN, "not_accepted"},
		{cV, "cosine", dist, "", ""},
		{cV, "mahalanobis", dist, "", ""},
		{cV, "jaccard", dist, perr.PULSE_VECTOR_METRIC_UNKNOWN, "no_form"},
		{cV, "chi_square", dist, perr.PULSE_VECTOR_METRIC_UNKNOWN, "not_accepted"},
		{cE, "std_euclidean", dist, perr.PULSE_VECTOR_METRIC_UNKNOWN, "not_accepted"},
		{cE, "cosine", sim, "", ""},
		{cE, "nope", sim, perr.PULSE_VECTOR_METRIC_UNKNOWN, "unregistered"},
		{cM, "hamming", dist, perr.PULSE_VECTOR_METRIC_UNKNOWN, "not_accepted"},
		{cM, "", dist, perr.PULSE_VECTOR_METRIC_REQUIRED, ""},
	}
	for _, tc := range cases {
		m, err := metric.ResolveFor(tc.c, tc.name, tc.form)
		if tc.code == "" {
			if err != nil || m.Name() != tc.name {
				t.Errorf("ResolveFor(%s, %q, %s) = %q, %v", tc.c, tc.name, tc.form, m.Name(), err)
			}
			continue
		}
		ce := codeOf(t, err)
		if ce.Code != tc.code || (tc.reason != "" && ce.Details["reason"] != tc.reason) {
			t.Errorf("ResolveFor(%s, %q, %s) = %s %v, want %s %s", tc.c, tc.name, tc.form, ce.Code, ce.Details["reason"], tc.code, tc.reason)
		}
		if ce.Details["consumer"] != string(tc.c) {
			t.Errorf("ResolveFor(%s, %q) consumer detail %v", tc.c, tc.name, ce.Details["consumer"])
		}
		valid, _ := ce.Details["valid"].([]string)
		for _, n := range valid {
			if !metric.Accepts(tc.c, n) {
				t.Errorf("ResolveFor(%s, %q) lists %s as valid, which %s does not accept", tc.c, tc.name, n, tc.c)
			}
		}
		if ce.Message == "" {
			t.Errorf("ResolveFor(%s, %q) has an empty message", tc.c, tc.name)
		}
	}
}

// TestZeroMetric: the zero Metric is invalid and answers its accessors
// with zero values.
func TestZeroMetric(t *testing.T) {
	var m metric.Metric
	if m.Valid() || m.Name() != "" || m.Fitted() || len(m.Forms()) != 0 || m.HasForm(metric.Distance) {
		t.Fatal("the zero Metric looks registered")
	}
	if _, ok := metric.Lookup("nope"); ok {
		t.Fatal("Lookup(nope) succeeded")
	}
	_, err := m.Eval(metric.Distance, []float64{1}, []float64{2}, nil)
	if ce := codeOf(t, err); ce.Code != perr.PULSE_VECTOR_METRIC_UNKNOWN {
		t.Fatalf("zero Metric Eval code %s", ce.Code)
	}
	_, err = m.EvalSets(metric.Similarity, metric.SetCounts{})
	if ce := codeOf(t, err); ce.Code != perr.PULSE_VECTOR_METRIC_UNKNOWN {
		t.Fatalf("zero Metric EvalSets code %s", ce.Code)
	}
}
