package metric

import (
	"slices"

	"github.com/frankbardon/pulse/linalg"
)

// Form is a direction a metric answers in.
type Form string

// The two forms. A similarity grows as the vectors get closer; a
// distance shrinks.
const (
	Similarity Form = "similarity"
	Distance   Form = "distance"
)

// Kind is what a vector's values mean, which decides the metrics that
// suit it.
type Kind string

// The vector kinds.
const (
	// Measure: magnitude matters (a load curve, spend per category).
	Measure Kind = "measure"
	// Scale: bounded ratings (a Likert battery).
	Scale Kind = "scale"
	// Composition: parts of a whole (share of wallet, channel mix).
	Composition Kind = "composition"
	// Binary: membership (a multi-select, a yes/no battery).
	Binary Kind = "binary"
)

// Kinds returns the vector kinds in canonical order.
func Kinds() []Kind { return []Kind{Measure, Scale, Composition, Binary} }

// Family groups the registry.
type Family string

// The metric families.
const (
	FamilyPlain  Family = "plain"
	FamilySet    Family = "set"
	FamilyFitted Family = "fitted"
)

// Domain is the value range a metric reads.
type Domain string

// The domains.
const (
	// DomainReal: any value.
	DomainReal Domain = "real"
	// DomainNonNegative: values ≥ 0.
	DomainNonNegative Domain = "non_negative"
	// DomainBinary: values 0 or 1.
	DomainBinary Domain = "binary"
)

// Fit names the precomputed statistic a fitted metric reads from Stats.
type Fit string

// The fitted statistics. FitNone marks a row-local metric.
const (
	FitNone              Fit = ""
	FitSD                Fit = "sd"
	FitInverseCovariance Fit = "inverse_covariance"
	FitColumnMasses      Fit = "column_masses"
)

// Stats carries the fitted statistics a fitted metric reads. The metric
// never estimates them: the caller's fit pass does.
type Stats struct {
	// SD is the per-element standard deviation std_euclidean divides
	// each difference by; every entry finite and > 0.
	SD []float64
	// InverseCovariance is the inverse covariance mahalanobis forms its
	// quadratic with; order = the vector length.
	InverseCovariance *linalg.Sym
	// ColumnMasses are the column masses chi_square weights each
	// squared profile difference by (1 / mass); every entry finite and
	// > 0.
	ColumnMasses []float64
}

// Consumer is a surface that resolves metrics through the registry.
// Built-in operator consumers are spelled as the operator.
type Consumer string

// The registry's consumers.
const (
	// ConsumerExpr: the expression metric functions — exactly the
	// metrics a function exposes: dist (euclidean), cosine (its
	// similarity form only), jaccard, dice, hamming and overlap.
	ConsumerExpr Consumer = "expr"
	// ConsumerVecDistance: ATTR_VEC_DISTANCE. Distance form; the
	// row-local metrics plus std_euclidean and mahalanobis.
	ConsumerVecDistance Consumer = "ATTR_VEC_DISTANCE"
	// ConsumerProfileSimilarity: ATTR_PROFILE_SIMILARITY. Similarity
	// form only.
	ConsumerProfileSimilarity Consumer = "ATTR_PROFILE_SIMILARITY"
	// ConsumerMatDistance: MAT_DISTANCE. Distance form between bucket
	// centroids.
	ConsumerMatDistance Consumer = "MAT_DISTANCE"
	// ConsumerKMeans: GROUP_KMEANS. The Euclidean family only: k-means
	// minimises squared Euclidean error.
	ConsumerKMeans Consumer = "GROUP_KMEANS"
)

// consumerForms is the form(s) each consumer asks for.
var consumerForms = map[Consumer][]Form{
	ConsumerExpr:              {Similarity, Distance},
	ConsumerVecDistance:       {Distance},
	ConsumerProfileSimilarity: {Similarity},
	ConsumerMatDistance:       {Distance},
	ConsumerKMeans:            {Distance},
}

// Consumers returns every registry consumer in canonical order.
func Consumers() []Consumer {
	return []Consumer{ConsumerExpr, ConsumerVecDistance, ConsumerProfileSimilarity, ConsumerMatDistance, ConsumerKMeans}
}

// Forms returns the form(s) the consumer asks for; nil for an unknown
// consumer.
func (c Consumer) Forms() []Form { return slices.Clone(consumerForms[c]) }

// definition is one registry entry.
type definition struct {
	name          string
	description   string
	family        Family
	direction     Form
	forms         []Form
	suited        []Kind
	poor          []Kind
	fit           Fit
	domain        Domain
	zeroUndefined bool
	scaled        bool
	complete      bool
	consumers     []Consumer
	// narrow restricts, per consumer, the forms that consumer may ask
	// the metric for (absent: every form the consumer asks for).
	narrow map[Consumer][]Form
	eval   func(d *definition, f Form, a, b []float64, s *Stats) (float64, error)
	sets   func(f Form, c SetCounts) float64
}

// Metric is one registered metric. The zero Metric is invalid (Valid
// reports false); obtain one from All, Lookup, Resolve or ResolveFor.
type Metric struct {
	def *definition
}

var (
	cE = ConsumerExpr
	cV = ConsumerVecDistance
	cP = ConsumerProfileSimilarity
	cM = ConsumerMatDistance
	cK = ConsumerKMeans
)

// registry is the v1 metric set, in canonical order.
var registry = []*definition{
	{
		name: "euclidean", family: FamilyPlain, direction: Distance, forms: []Form{Distance},
		description: "sqrt(sum((a-b)^2)), the sum rescaled by p/p_observed",
		suited:      []Kind{Measure, Scale}, poor: []Kind{Composition},
		domain: DomainReal, scaled: true,
		consumers: []Consumer{cE, cV, cM, cK}, eval: evalSquares,
	},
	{
		name: "sq_euclidean", family: FamilyPlain, direction: Distance, forms: []Form{Distance},
		description: "sum((a-b)^2), rescaled by p/p_observed",
		suited:      []Kind{Measure, Scale}, poor: []Kind{Composition},
		domain: DomainReal, scaled: true,
		consumers: []Consumer{cV, cM, cK}, eval: evalSquares,
	},
	{
		name: "manhattan", family: FamilyPlain, direction: Distance, forms: []Form{Distance},
		description: "sum(|a-b|), rescaled by p/p_observed",
		suited:      []Kind{Measure, Composition}, poor: []Kind{},
		domain: DomainReal, scaled: true,
		consumers: []Consumer{cV, cM}, eval: evalManhattan,
	},
	{
		name: "chebyshev", family: FamilyPlain, direction: Distance, forms: []Form{Distance},
		description: "max(|a-b|)",
		suited:      []Kind{Measure}, poor: []Kind{},
		domain:    DomainReal,
		consumers: []Consumer{cV, cM}, eval: evalChebyshev,
	},
	{
		name: "cosine", family: FamilyPlain, direction: Similarity, forms: []Form{Similarity, Distance},
		description: "similarity a.b/(|a||b|); distance 1-cos clamped to [0,2]",
		suited:      []Kind{Composition, Binary}, poor: []Kind{Measure, Scale},
		domain:    DomainReal,
		consumers: []Consumer{cE, cV, cP, cM}, narrow: map[Consumer][]Form{cE: {Similarity}}, eval: evalCosine,
	},
	{
		name: "correlation", family: FamilyPlain, direction: Similarity, forms: []Form{Similarity},
		description: "Pearson r of the two vectors' elements (centered cosine)",
		suited:      []Kind{Scale}, poor: []Kind{},
		domain:    DomainReal,
		consumers: []Consumer{cP}, eval: evalCorrelation,
	},
	{
		name: "hellinger", family: FamilyPlain, direction: Distance, forms: []Form{Distance},
		description: "sqrt(sum((sqrt(a/sum a)-sqrt(b/sum b))^2)/2), in [0,1]",
		suited:      []Kind{Composition}, poor: []Kind{},
		domain:    DomainNonNegative,
		consumers: []Consumer{cV, cM}, eval: evalHellinger,
	},
	{
		name: "aitchison", family: FamilyPlain, direction: Distance, forms: []Form{Distance},
		description: "Euclidean distance between the centered log-ratio transforms; a zero part is undefined",
		suited:      []Kind{Composition}, poor: []Kind{},
		domain: DomainNonNegative, zeroUndefined: true,
		consumers: []Consumer{cV, cM}, eval: evalAitchison,
	},
	{
		name: "jaccard", family: FamilySet, direction: Similarity, forms: []Form{Similarity},
		description: "|A and B| / |A or B|",
		suited:      []Kind{Binary}, poor: []Kind{},
		domain:    DomainBinary,
		consumers: []Consumer{cE, cP}, eval: evalSet, sets: setJaccard,
	},
	{
		name: "dice", family: FamilySet, direction: Similarity, forms: []Form{Similarity},
		description: "2|A and B| / (|A|+|B|)",
		suited:      []Kind{Binary}, poor: []Kind{},
		domain:    DomainBinary,
		consumers: []Consumer{cE, cP}, eval: evalSet, sets: setDice,
	},
	{
		name: "hamming", family: FamilySet, direction: Distance, forms: []Form{Distance},
		description: "the number of elements in exactly one set, rescaled by p/p_observed",
		suited:      []Kind{Binary}, poor: []Kind{},
		domain: DomainBinary, scaled: true,
		consumers: []Consumer{cE, cV}, eval: evalSet, sets: setHamming,
	},
	{
		name: "overlap", family: FamilySet, direction: Similarity, forms: []Form{Similarity},
		description: "|A and B| / min(|A|,|B|)",
		suited:      []Kind{Binary}, poor: []Kind{},
		domain:    DomainBinary,
		consumers: []Consumer{cE, cP}, eval: evalSet, sets: setOverlap,
	},
	{
		name: "std_euclidean", family: FamilyFitted, direction: Distance, forms: []Form{Distance},
		description: "sqrt(sum(((a-b)/sd)^2)) over fitted per-element SDs, the sum rescaled by p/p_observed",
		suited:      []Kind{Measure}, poor: []Kind{Composition},
		fit: FitSD, domain: DomainReal, scaled: true,
		consumers: []Consumer{cV}, eval: evalStdEuclidean,
	},
	{
		name: "mahalanobis", family: FamilyFitted, direction: Distance, forms: []Form{Distance},
		description: "sqrt(d' S^-1 d), d = a-b, over a fitted inverse covariance; needs every element",
		suited:      []Kind{Measure}, poor: []Kind{},
		fit: FitInverseCovariance, domain: DomainReal, complete: true,
		consumers: []Consumer{cV, cM}, eval: evalMahalanobis,
	},
	{
		name: "chi_square", family: FamilyFitted, direction: Distance, forms: []Form{Distance},
		description: "sqrt(sum((a/sum a - b/sum b)^2 / mass)) over fitted column masses (correspondence-analysis profile distance)",
		suited:      []Kind{Composition}, poor: []Kind{},
		fit: FitColumnMasses, domain: DomainNonNegative,
		consumers: []Consumer{}, eval: evalChiSquare,
	},
}

// byName indexes the registry.
var byName = func() map[string]*definition {
	m := make(map[string]*definition, len(registry))
	for _, d := range registry {
		m[d.name] = d
	}
	return m
}()

// All returns every registered metric in canonical order.
func All() []Metric {
	out := make([]Metric, len(registry))
	for i, d := range registry {
		out[i] = Metric{def: d}
	}
	return out
}

// Names returns every registered metric name in canonical order.
func Names() []string {
	out := make([]string, len(registry))
	for i, d := range registry {
		out[i] = d.name
	}
	return out
}

// Lookup returns the metric registered as name.
func Lookup(name string) (Metric, bool) {
	d, ok := byName[name]
	return Metric{def: d}, ok
}

// namesWith lists, in canonical order, the metrics keep accepts.
func namesWith(keep func(*definition) bool) []string {
	out := []string{}
	for _, d := range registry {
		if keep(d) {
			out = append(out, d.name)
		}
	}
	return out
}

// Resolve returns the metric named name for a caller that needs form
// f. An empty name is PULSE_VECTOR_METRIC_REQUIRED; an unregistered
// name, or a metric with no form f, is PULSE_VECTOR_METRIC_UNKNOWN.
func Resolve(name string, f Form) (Metric, error) {
	valid := namesWith(func(d *definition) bool { return slices.Contains(d.forms, f) })
	if name == "" {
		return Metric{}, required(f, "", valid)
	}
	d, ok := byName[name]
	if !ok {
		return Metric{}, refusal(name, reasonUnregistered, f, "", valid)
	}
	if !slices.Contains(d.forms, f) {
		return Metric{}, refusal(name, reasonNoForm, f, "", valid)
	}
	return Metric{def: d}, nil
}

// ResolveFor is Resolve for consumer c: the metric must also be one c
// accepts, and f one of the forms c asks for. Every refusal is
// PULSE_VECTOR_METRIC_UNKNOWN (an empty name is
// PULSE_VECTOR_METRIC_REQUIRED), its "valid" detail the names c accepts
// in form f.
func ResolveFor(c Consumer, name string, f Form) (Metric, error) {
	valid := namesWith(func(d *definition) bool { return d.serves(c, f) })
	if name == "" {
		return Metric{}, required(f, c, valid)
	}
	d, ok := byName[name]
	switch {
	case !ok:
		return Metric{}, refusal(name, reasonUnregistered, f, c, valid)
	case !slices.Contains(d.forms, f):
		return Metric{}, refusal(name, reasonNoForm, f, c, valid)
	case !slices.Contains(valid, name):
		return Metric{}, refusal(name, reasonNotAccepted, f, c, valid)
	}
	return Metric{def: d}, nil
}

// serves reports whether consumer c accepts d in form f: c asks for f,
// d answers in f, c is one of d's consumers and no per-consumer
// narrowing excludes f.
func (d *definition) serves(c Consumer, f Form) bool {
	if !slices.Contains(consumerForms[c], f) || !slices.Contains(d.consumers, c) || !slices.Contains(d.forms, f) {
		return false
	}
	if forms, ok := d.narrow[c]; ok {
		return slices.Contains(forms, f)
	}
	return true
}

// Accepts reports whether consumer c accepts the metric name (in any
// form c asks for).
func Accepts(c Consumer, name string) bool {
	d, ok := byName[name]
	return ok && slices.Contains(d.consumers, c)
}

// Accepted returns, in canonical order, every metric consumer c
// accepts.
func Accepted(c Consumer) []string {
	return namesWith(func(d *definition) bool { return slices.Contains(d.consumers, c) })
}

// Valid reports whether m is a registered metric (not the zero Metric).
func (m Metric) Valid() bool { return m.def != nil }

// Name is the registered name ("" for the zero Metric).
func (m Metric) Name() string {
	if m.def == nil {
		return ""
	}
	return m.def.name
}

// Description is a one-line formula.
func (m Metric) Description() string { return m.get().description }

// Family is the registry group.
func (m Metric) Family() Family { return m.get().family }

// Direction is the native form: what the metric's own name means.
func (m Metric) Direction() Form { return m.get().direction }

// Forms lists every form the metric can answer in, native first.
func (m Metric) Forms() []Form { return slices.Clone(m.get().forms) }

// HasForm reports whether the metric can answer in form f.
func (m Metric) HasForm(f Form) bool { return slices.Contains(m.get().forms, f) }

// KindsSuited lists the vector kinds the metric suits.
func (m Metric) KindsSuited() []Kind { return slices.Clone(m.get().suited) }

// PoorFit lists the vector kinds the metric fits poorly — the pairs a
// kind-aware consumer warns about.
func (m Metric) PoorFit() []Kind { return slices.Clone(m.get().poor) }

// PoorFitFor reports whether the metric fits kind k poorly.
func (m Metric) PoorFitFor(k Kind) bool { return slices.Contains(m.get().poor, k) }

// Fitted reports whether the metric reads fitted statistics.
func (m Metric) Fitted() bool { return m.get().fit != FitNone }

// Fit names the statistic a fitted metric reads (FitNone when
// row-local).
func (m Metric) Fit() Fit { return m.get().fit }

// Domain is the value range the metric reads.
func (m Metric) Domain() Domain { return m.get().domain }

// ZeroUndefined reports whether a zero element makes the value
// undefined (aitchison).
func (m Metric) ZeroUndefined() bool { return m.get().zeroUndefined }

// Scaled reports whether the element sum is rescaled by p/p_observed.
func (m Metric) Scaled() bool { return m.get().scaled }

// Complete reports whether the metric needs every element (any missing
// element gives NaN).
func (m Metric) Complete() bool { return m.get().complete }

// Consumers lists, in canonical order, every consumer that accepts the
// metric. Empty for a registered-but-unexposed metric (chi_square).
func (m Metric) Consumers() []Consumer { return slices.Clone(m.get().consumers) }

// zero is the definition the zero Metric answers its accessors from.
var zero = &definition{}

func (m Metric) get() *definition {
	if m.def == nil {
		return zero
	}
	return m.def
}

// kindDefaults is the kind → default metric table, per form. A kind
// with no default in a form (measure has no similarity default) is
// absent from that form's map.
var kindDefaults = map[Form]map[Kind]string{
	Distance:   {Measure: "std_euclidean", Scale: "euclidean", Composition: "aitchison", Binary: "hamming"},
	Similarity: {Scale: "correlation", Composition: "cosine", Binary: "jaccard"},
}

// Default returns the metric a vector of kind k defaults to in form f;
// false when the kind has no default in that form (measure has no
// similarity default) or k is not a kind.
func Default(k Kind, f Form) (Metric, bool) {
	name, ok := kindDefaults[f][k]
	if !ok {
		return Metric{}, false
	}
	return Metric{def: byName[name]}, true
}

// DefaultFor is Default for consumer c: the kind's default when c
// accepts it in form f, else the first metric in canonical order that c
// accepts in form f, that suits k and for which admit (nil admits every
// metric) reports true. A poor fit is never chosen. False when none
// qualifies.
func DefaultFor(c Consumer, k Kind, f Form, admit func(Metric) bool) (Metric, bool) {
	ok := func(d *definition) bool {
		return d.serves(c, f) && slices.Contains(d.suited, k) && !slices.Contains(d.poor, k) &&
			(admit == nil || admit(Metric{def: d}))
	}
	if m, has := Default(k, f); has && ok(m.def) {
		return m, true
	}
	for _, d := range registry {
		if ok(d) {
			return Metric{def: d}, true
		}
	}
	return Metric{}, false
}
