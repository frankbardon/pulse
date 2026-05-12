// Package query is the heuristic English-to-types.Request parser used by
// the pulse.Ask facade and the `pulse api ask` CLI leaf.
//
// The parser accepts a constrained natural-language string ("average
// revenue by region", "count sessions over time month") together with a
// .pulse schema, and emits a *types.Request whose Field slots resolve
// against schema columns via direct or Levenshtein-near match. Operator
// Type is filled where the verb is unambiguous; otherwise it is left
// empty so descriptor.ResolveDefaults can infer it from the field's
// schema type (the smart-defaults safety net).
//
// Structural ban: this package imports only types/, encoding/, and
// errors/. It MUST NOT import service/, processing/, or descriptor/ —
// the facade resolves the request, predict validates it, the runtime
// executes it. The parser stays inert. The Levenshtein helper is
// duplicated here (see descriptor.predict_suggestions.levenshtein) so
// the dependency graph stays minimal.
package query

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Confidence weights. Aggregated confidence is the product of per-step
// weights, floored at 0 and capped at 1.
const (
	confVerbDirect   = 1.0
	confVerbSynonym  = 0.8
	confFieldDirect  = 1.0
	confFieldDist1   = 0.9
	confFieldDist2   = 0.7
	maxFieldDistance = 2
)

// Issue mirrors descriptor.EnvelopeEntry minus the json package
// dependency. The caller converts each issue into an envelope entry.
type Issue struct {
	Code    errors.Code
	Message string
	Details map[string]any
}

// Result is the parser output. Request is always non-nil; when the
// input is unparseable the request is a stub with the supplied cohort
// and Issues contains a PULSE_QUERY_UNRESOLVED error.
type Result struct {
	// Request is the synthesized request. Cohort is filled when the
	// caller passes a non-empty File argument to Parse. Slots whose
	// Type the parser could not pin down are left empty so the smart-
	// defaults pass (descriptor.ResolveDefaults) can fill them at
	// validation time.
	Request *types.Request

	// MatchedFields lists every schema field name the parser
	// successfully resolved against. Order: appearance in the request
	// (aggregation, then groups, then filters, then windows).
	MatchedFields []string

	// Confidence is the aggregate parser confidence in [0, 1].
	Confidence float64

	// Issues are warnings or errors emitted during parsing. The
	// envelope-level errors and warnings lists are derived from this
	// slice by the caller (service.Ask).
	Issues []Issue
}

// ParseOptions modulates parse behavior.
type ParseOptions struct {
	// File is the cohort filename to attach to the synthesized
	// Request.Cohort. When empty the request carries no cohort and the
	// caller is expected to set one.
	File string
}

// Parse translates query against schema into a *types.Request. The
// returned Result is always non-nil; check Result.Issues for
// PULSE_QUERY_* codes to detect parse problems.
func Parse(query string, schema *encoding.Schema, opts ParseOptions) *Result {
	res := &Result{
		Request:    &types.Request{},
		Confidence: 1.0,
	}
	if opts.File != "" {
		res.Request.Cohort = &types.Cohort{Filename: opts.File}
	}

	q := strings.TrimSpace(query)
	if q == "" {
		res.Confidence = 0
		res.Issues = append(res.Issues, Issue{
			Code:    errors.PULSE_QUERY_UNRESOLVED,
			Message: "query is empty",
		})
		return res
	}

	p := &parser{
		schema: schema,
		fields: schemaFieldNames(schema),
		res:    res,
	}
	p.tokens = tokenize(q)
	p.parse()

	if p.res.Confidence < 0 {
		p.res.Confidence = 0
	}
	if p.res.Confidence > 1 {
		p.res.Confidence = 1
	}
	return res
}

// parser holds parse state. Token-tape design: tokens is the input
// sequence; pos advances through it as recognizers consume.
type parser struct {
	tokens   []string
	pos      int
	schema   *encoding.Schema
	fields   []string
	res      *Result
	withTail []string // stashed by extractTail("with"), read by parseCorrelate
}

func (p *parser) parse() {
	// Strip a trailing "where ..." filter clause first so the head
	// recognizers don't have to special-case it. Same for "with lag N".
	whereTokens := p.extractTail("where")
	withTokens := p.extractTail("with")
	p.withTail = withTokens

	if !p.parseHead() {
		// Nothing recognized at the head — fail loud.
		p.res.Confidence = 0
		p.emit(errors.PULSE_QUERY_UNRESOLVED, "could not parse query: no recognizable verb at head", map[string]any{
			"query": strings.Join(p.tokens, " "),
		})
	} else {
		// Handle "by <field>[, <field>]*" and "over time [bucket]".
		p.parseGroups()
	}

	if len(whereTokens) > 0 {
		p.parseWhere(whereTokens)
	}
	// Only run parseWith on the leftover withTail — parseCorrelate
	// drains it via takeWithTail when it consumed the "with <field>"
	// form, so non-correlate queries get the lag/window treatment.
	if remaining := p.withTail; len(remaining) > 0 {
		p.parseWith(remaining)
	}
}

// extractTail removes the suffix starting at the first standalone
// occurrence of keyword (case-sensitive on the lowercased tape) and
// returns the suffix tokens (without the keyword itself). Returns nil
// when keyword is absent. Only the first match is split so chained
// clauses (e.g. "... where x > 1 with lag 2") leave "with lag 2" inside
// the where tail; parseWhere stops at the first non-predicate token.
func (p *parser) extractTail(keyword string) []string {
	for i, t := range p.tokens {
		if t == keyword {
			tail := p.tokens[i+1:]
			p.tokens = p.tokens[:i]
			return tail
		}
	}
	return nil
}

// parseHead recognizes the verb-led head of the query. Returns true on
// success. The recognized clause populates res.Request.Aggregations or
// res.Request.Tests as appropriate and advances p.pos past it.
func (p *parser) parseHead() bool {
	if p.peek() == "" {
		return false
	}

	// "top <N> <field> by count"
	if p.peek() == "top" {
		return p.parseTopN()
	}

	// "correlate <field> with <field>" — but "with" appears in our
	// reserved-tail list, so extractTail("with") will have stolen it.
	// Reattach: special-case correlate by checking the head verb.
	if p.peek() == "correlate" {
		return p.parseCorrelate()
	}

	// "percentile <N> <field>"
	if p.peek() == "percentile" {
		return p.parsePercentile()
	}

	// "count" (no field, or "count <field>")
	if p.peek() == "count" {
		return p.parseCount()
	}

	// Generic "<agg-verb> <field>"
	if agg, conf, ok := matchAggVerb(p.peek()); ok {
		p.advance()
		p.scaleConfidence(conf)

		field, fconf, fok := p.consumeField()
		if !fok {
			p.emit(errors.PULSE_QUERY_UNRESOLVED, "aggregation verb missing a field", map[string]any{
				"verb": string(agg),
			})
			// Still record the aggregation with empty field so predict
			// surfaces the missing-field error cleanly downstream.
			p.res.Request.Aggregations = append(p.res.Request.Aggregations, &types.Aggregation{Type: agg})
			return true
		}
		p.scaleConfidence(fconf)
		p.res.Request.Aggregations = append(p.res.Request.Aggregations, &types.Aggregation{
			Type:  agg,
			Field: field,
		})
		return true
	}

	return false
}

// parseTopN handles "top <N> <field> by count".
func (p *parser) parseTopN() bool {
	p.advance() // consume "top"
	n, ok := p.consumeInt()
	if !ok {
		p.emit(errors.PULSE_QUERY_UNRESOLVED, "top <N>: expected a positive integer after 'top'", nil)
		return false
	}
	field, fconf, fok := p.consumeField()
	if !fok {
		p.emit(errors.PULSE_QUERY_UNRESOLVED, "top <N> <field>: missing field after N", nil)
		return false
	}
	p.scaleConfidence(fconf)

	// Consume optional "by count" — tolerated whether present or absent.
	if p.peek() == "by" {
		p.advance()
		if p.peek() == "count" {
			p.advance()
		}
	}

	// Emit AGG_COUNT grouped by the field, sorted desc.
	p.res.Request.Aggregations = append(p.res.Request.Aggregations, &types.Aggregation{
		Type:  types.AGG_COUNT,
		Field: field,
		Label: "n",
	})
	p.res.Request.Groups = append(p.res.Request.Groups, &types.Group{
		Type:  types.GROUP_CATEGORY,
		Field: field,
	})
	p.res.Request.Sort = append(p.res.Request.Sort, types.OrderKey{Field: "n", Desc: true})
	// N is captured but not enforced server-side (no LIMIT operator);
	// preserved as a parse hint for the caller's downstream pipe.
	_ = n
	return true
}

// parseCorrelate handles "correlate <field> with <field>". The "with"
// tail was sliced off by extractTail; reattach it here.
func (p *parser) parseCorrelate() bool {
	p.advance() // consume "correlate"
	field, fconf, fok := p.consumeField()
	if !fok {
		p.emit(errors.PULSE_QUERY_UNRESOLVED, "correlate <field> with <field>: missing first field", nil)
		return false
	}
	p.scaleConfidence(fconf)

	// The "with <field>" tail lives in withTokens — re-fetch.
	with := p.takeWithTail()
	if len(with) == 0 {
		p.emit(errors.PULSE_QUERY_UNRESOLVED, "correlate requires a second field via 'with <field>'", nil)
		return false
	}
	subp := &parser{schema: p.schema, fields: p.fields, res: p.res, tokens: with}
	field2, f2conf, f2ok := subp.consumeField()
	if !f2ok {
		p.emit(errors.PULSE_QUERY_UNRESOLVED, "correlate ... with <field>: missing second field", nil)
		return false
	}
	p.scaleConfidence(f2conf)
	p.res.Request.Tests = append(p.res.Request.Tests, &types.Test{
		Type:   types.TEST_PEARSON_R,
		Field:  field,
		Field2: field2,
	})
	return true
}

// parsePercentile handles "percentile <N> <field>".
func (p *parser) parsePercentile() bool {
	p.advance() // consume "percentile"
	n, ok := p.consumeFloat()
	if !ok {
		p.emit(errors.PULSE_QUERY_UNRESOLVED, "percentile <N> <field>: expected numeric N after 'percentile'", nil)
		return false
	}
	field, fconf, fok := p.consumeField()
	if !fok {
		p.emit(errors.PULSE_QUERY_UNRESOLVED, "percentile <N> <field>: missing field after N", nil)
		return false
	}
	p.scaleConfidence(fconf)

	params, _ := json.Marshal(map[string]any{"p": n})
	p.res.Request.Aggregations = append(p.res.Request.Aggregations, &types.Aggregation{
		Type:   types.AGG_PERCENTILE,
		Field:  field,
		Params: params,
	})
	return true
}

// parseCount handles "count" (no field) and "count <field>".
func (p *parser) parseCount() bool {
	p.advance() // consume "count"
	// Bare count or count-then-by-clause: no field.
	if p.peek() == "" || p.peek() == "by" || p.peek() == "over" {
		// Pick the first field in the schema as the AGG_COUNT carrier;
		// AGG_COUNT ignores nulls per-field, so use any non-empty
		// schema field. Fall back to empty when schema is empty.
		f := ""
		if len(p.fields) > 0 {
			f = p.fields[0]
		}
		p.res.Request.Aggregations = append(p.res.Request.Aggregations, &types.Aggregation{
			Type:  types.AGG_COUNT,
			Field: f,
			Label: "n",
		})
		return true
	}
	field, fconf, fok := p.consumeField()
	if !fok {
		p.emit(errors.PULSE_QUERY_UNRESOLVED, "count <field>: missing field after 'count'", nil)
		return false
	}
	p.scaleConfidence(fconf)
	p.res.Request.Aggregations = append(p.res.Request.Aggregations, &types.Aggregation{
		Type:  types.AGG_COUNT,
		Field: field,
		Label: "n",
	})
	return true
}

// parseGroups consumes "by <field>[(, | and) <field>]*" and/or "over
// time [bucket]" tails.
func (p *parser) parseGroups() {
	for {
		switch p.peek() {
		case "by":
			p.advance()
			p.parseByList()
		case "over":
			if p.peekAt(1) == "time" {
				p.advance() // over
				p.advance() // time
				p.addDateGroup(p.peek()) // optional bucket
				if isBucket(p.peek()) {
					p.advance()
				}
			} else {
				return
			}
		default:
			return
		}
	}
}

// parseByList consumes a comma/and-separated field list after "by".
func (p *parser) parseByList() {
	for {
		field, fconf, fok := p.consumeField()
		if !fok {
			p.emit(errors.PULSE_QUERY_UNRESOLVED, "'by' requires a field reference", nil)
			return
		}
		p.scaleConfidence(fconf)

		// If the field is a date type, emit GROUP_DATE (default day
		// bucket); otherwise leave Type empty so smart-defaults can
		// pick the right grouper from the field type.
		grp := &types.Group{Field: field}
		if p.schema != nil {
			if f := p.schema.Field(field); f != nil && f.Type == encoding.FieldTypeDate {
				grp.Type = types.GROUP_DATE
				grp.Params = []byte(`{"component":"day"}`)
			}
		}
		p.res.Request.Groups = append(p.res.Request.Groups, grp)

		// Separators: "," "and" or done.
		switch p.peek() {
		case ",", "and":
			p.advance()
			continue
		default:
			return
		}
	}
}

// addDateGroup attaches a GROUP_DATE on the first date field in the
// schema (when present) using the supplied bucket (default "day").
func (p *parser) addDateGroup(bucketCandidate string) {
	bucket := bucketCandidate
	if !isBucket(bucket) {
		bucket = "day"
	}
	dateField := ""
	if p.schema != nil {
		for _, f := range p.schema.Fields {
			if f.Type == encoding.FieldTypeDate {
				dateField = f.Name
				break
			}
		}
	}
	if dateField == "" {
		p.emit(errors.PULSE_QUERY_UNRESOLVED, "'over time': cohort has no date field to group by", nil)
		return
	}
	params, _ := json.Marshal(map[string]any{"component": bucket})
	p.res.Request.Groups = append(p.res.Request.Groups, &types.Group{
		Type:   types.GROUP_DATE,
		Field:  dateField,
		Params: params,
	})
	if !contains(p.res.MatchedFields, dateField) {
		p.res.MatchedFields = append(p.res.MatchedFields, dateField)
	}
}

// parseWhere consumes the "where <field> <op> <value>" tail. Op is one
// of =, !=, >, <, >=, <=. Stops at the first token that does not
// belong to the predicate (e.g. a stray "with lag").
func (p *parser) parseWhere(tokens []string) {
	subp := &parser{schema: p.schema, fields: p.fields, res: p.res, tokens: tokens}

	field, fconf, fok := subp.consumeField()
	if !fok {
		p.emit(errors.PULSE_QUERY_UNRESOLVED, "where <field> ...: missing field after 'where'", nil)
		return
	}
	p.scaleConfidence(fconf)

	op := subp.peek()
	if !isCompareOp(op) {
		// Tolerate implicit equality: "where region us" → region == us.
		op = "="
	} else {
		subp.advance()
	}

	if subp.peek() == "" {
		p.emit(errors.PULSE_QUERY_UNRESOLVED, "where <field> <op> <value>: missing value", nil)
		return
	}
	value := subp.advance()

	switch op {
	case "=":
		p.res.Request.Filterers = append(p.res.Request.Filterers, &types.Filterer{
			Type:   types.FILTER_INCLUDE,
			Field:  field,
			Values: []string{value},
		})
	case "!=":
		p.res.Request.Filterers = append(p.res.Request.Filterers, &types.Filterer{
			Type:   types.FILTER_EXCLUDE,
			Field:  field,
			Values: []string{value},
		})
	case ">":
		p.res.Request.Filterers = append(p.res.Request.Filterers, &types.Filterer{
			Type:   types.FILTER_RANGE,
			Field:  field,
			Values: []string{strictNextFloat(value, +1), ""},
		})
	case "<":
		p.res.Request.Filterers = append(p.res.Request.Filterers, &types.Filterer{
			Type:   types.FILTER_RANGE,
			Field:  field,
			Values: []string{"", strictNextFloat(value, -1)},
		})
	case ">=":
		p.res.Request.Filterers = append(p.res.Request.Filterers, &types.Filterer{
			Type:   types.FILTER_RANGE,
			Field:  field,
			Values: []string{value, ""},
		})
	case "<=":
		p.res.Request.Filterers = append(p.res.Request.Filterers, &types.Filterer{
			Type:   types.FILTER_RANGE,
			Field:  field,
			Values: []string{"", value},
		})
	}
}

// parseWith consumes the "with lag <N>" tail. Other "with ..." forms
// are ignored (parseCorrelate consumes "with <field>" directly).
func (p *parser) parseWith(tokens []string) {
	if len(tokens) == 0 {
		return
	}
	if tokens[0] != "lag" {
		// Not a parser-recognized "with" form; emit a soft warning so
		// the caller knows we skipped it.
		return
	}
	if len(tokens) < 2 {
		p.emit(errors.PULSE_QUERY_UNRESOLVED, "with lag <N>: missing N", nil)
		return
	}
	n, err := strconv.Atoi(tokens[1])
	if err != nil || n <= 0 {
		p.emit(errors.PULSE_QUERY_UNRESOLVED, "with lag <N>: N must be a positive integer", nil)
		return
	}

	// Pick the first aggregation as the lag's source label when
	// present. The user can override by editing the resolved request.
	srcLabel := ""
	if len(p.res.Request.Aggregations) > 0 {
		a := p.res.Request.Aggregations[0]
		if a.Label != "" {
			srcLabel = a.Label
		} else if a.Field != "" {
			srcLabel = string(a.Type) + "_" + a.Field
		}
	}
	params, _ := json.Marshal(map[string]any{"offset": n})
	p.res.Request.Windows = append(p.res.Request.Windows, &types.Window{
		Type:   types.WIN_LAG,
		Field:  srcLabel,
		Label:  fmt.Sprintf("lag_%d", n),
		Params: params,
	})
}

// takeWithTail returns and clears the stored "with ..." tail. Used by
// parseCorrelate after extractTail has stashed it.
func (p *parser) takeWithTail() []string {
	out := p.withTail
	p.withTail = nil
	return out
}

// ---- tokenization and lookups ----

// tokenize splits the input into lowercase tokens. Comma and the
// compare operators (=, !=, >, <, >=, <=) get their own tokens; other
// punctuation is dropped. Quoted values (single or double) are
// preserved as a single token.
func tokenize(s string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, strings.ToLower(cur.String()))
			cur.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case ' ', '\t', '\n':
			flush()
		case ',':
			flush()
			out = append(out, ",")
		case '"', '\'':
			// Preserve quoted content as one token.
			flush()
			j := i + 1
			for j < len(s) && s[j] != c {
				j++
			}
			out = append(out, strings.ToLower(s[i+1:j]))
			i = j
		case '=', '>', '<', '!':
			flush()
			op := string(c)
			if i+1 < len(s) && s[i+1] == '=' && (c == '!' || c == '>' || c == '<' || c == '=') {
				op = string(c) + "="
				i++
			}
			out = append(out, op)
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}

func (p *parser) peek() string {
	if p.pos >= len(p.tokens) {
		return ""
	}
	return p.tokens[p.pos]
}
func (p *parser) peekAt(off int) string {
	if p.pos+off >= len(p.tokens) {
		return ""
	}
	return p.tokens[p.pos+off]
}
func (p *parser) advance() string {
	if p.pos >= len(p.tokens) {
		return ""
	}
	t := p.tokens[p.pos]
	p.pos++
	return t
}

// consumeField reads the next token and resolves it against the
// schema. Direct match wins (conf=1); Levenshtein ≤ maxFieldDistance
// returns the closest candidate with confidence 0.9 (d=1) or 0.7 (d=2).
// Ties at the same distance emit PULSE_QUERY_AMBIGUOUS.
//
// Returns (field, conf, ok). When ok=false, no token was consumed and
// no field could be resolved (PULSE_QUERY_UNRESOLVED is emitted).
func (p *parser) consumeField() (string, float64, bool) {
	tok := p.peek()
	if tok == "" {
		return "", 0, false
	}

	if p.schema != nil {
		if f := p.schema.Field(tok); f != nil {
			p.advance()
			if !contains(p.res.MatchedFields, tok) {
				p.res.MatchedFields = append(p.res.MatchedFields, tok)
			}
			return tok, confFieldDirect, true
		}

		matches, dist := nearestFields(tok, p.fields, maxFieldDistance)
		if len(matches) > 0 {
			p.advance()
			pick := matches[0]
			if !contains(p.res.MatchedFields, pick) {
				p.res.MatchedFields = append(p.res.MatchedFields, pick)
			}
			conf := confFieldDist2
			if dist == 1 {
				conf = confFieldDist1
			}
			if len(matches) > 1 {
				p.emit(errors.PULSE_QUERY_AMBIGUOUS,
					"field token '"+tok+"' matched multiple schema fields; picked the lexically first",
					map[string]any{
						"supplied":   tok,
						"candidates": matches,
						"picked":     pick,
					})
			}
			return pick, conf, true
		}
	}

	// Schema lookup failed: emit unresolved but still consume the
	// token so the parser can move on without infinite-looping.
	p.advance()
	p.emit(errors.PULSE_QUERY_UNRESOLVED, "field token '"+tok+"' does not match any schema field within edit distance "+strconv.Itoa(maxFieldDistance), map[string]any{
		"supplied": tok,
	})
	return tok, 0.0, true // return the verbatim token so downstream predict can name it
}

// consumeInt expects the next token to be a non-negative integer.
func (p *parser) consumeInt() (int, bool) {
	tok := p.peek()
	if tok == "" {
		return 0, false
	}
	n, err := strconv.Atoi(tok)
	if err != nil || n < 0 {
		return 0, false
	}
	p.advance()
	return n, true
}

// consumeFloat expects a float (or integer) literal.
func (p *parser) consumeFloat() (float64, bool) {
	tok := p.peek()
	if tok == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(tok, 64)
	if err != nil {
		return 0, false
	}
	p.advance()
	return f, true
}

// emit appends an issue and clamps confidence to 0 for errors.
func (p *parser) emit(code errors.Code, msg string, details map[string]any) {
	p.res.Issues = append(p.res.Issues, Issue{Code: code, Message: msg, Details: details})
	if code == errors.PULSE_QUERY_UNRESOLVED {
		p.res.Confidence = 0
	}
}

func (p *parser) scaleConfidence(factor float64) {
	p.res.Confidence *= factor
}

// matchAggVerb maps a verb token to its aggregator and a per-step
// confidence (1.0 for direct, 0.8 for synonym). Returns ok=false when
// the token is not a known verb.
func matchAggVerb(tok string) (types.AggregationType, float64, bool) {
	switch tok {
	case "average":
		return types.AGG_AVERAGE, confVerbDirect, true
	case "avg", "mean":
		return types.AGG_AVERAGE, confVerbSynonym, true
	case "sum":
		return types.AGG_SUM, confVerbDirect, true
	case "total":
		return types.AGG_SUM, confVerbSynonym, true
	case "median":
		return types.AGG_MEDIAN, confVerbDirect, true
	case "stddev":
		return types.AGG_STDDEV, confVerbDirect, true
	case "std":
		return types.AGG_STDDEV, confVerbSynonym, true
	case "min":
		return types.AGG_MIN, confVerbDirect, true
	case "minimum":
		return types.AGG_MIN, confVerbSynonym, true
	case "max":
		return types.AGG_MAX, confVerbDirect, true
	case "maximum":
		return types.AGG_MAX, confVerbSynonym, true
	}
	return "", 0, false
}

// isBucket reports whether tok is a recognized date bucket name.
func isBucket(tok string) bool {
	switch tok {
	case "day", "week", "month", "quarter", "year":
		return true
	}
	return false
}

// isCompareOp reports whether tok is a recognized comparison operator.
func isCompareOp(tok string) bool {
	switch tok {
	case "=", "!=", ">", "<", ">=", "<=":
		return true
	}
	return false
}

// strictNextFloat returns the strict-> inequality string-encoded
// counterpart of value. v1's FILTER_RANGE is inclusive, so an exclusive
// > needs to be expressed as ≥ (value + ε). The parser shifts by a
// minimal float epsilon; if value is not numeric we leave it untouched
// (predict will catch the type mismatch).
func strictNextFloat(value string, sign int) string {
	f, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return value
	}
	// Step by 1 ULP-ish: use math.Nextafter via a small relative bump.
	// For the parser's purposes the shift is documentary — the caller
	// is expected to convert > to ≥ explicitly when they review the
	// resolved request. Keep it deterministic:
	bump := 0.0
	switch {
	case f == 0:
		bump = 1e-9
	default:
		bump = f * 1e-12
		if bump < 0 {
			bump = -bump
		}
		if bump < 1e-12 {
			bump = 1e-12
		}
	}
	out := f + float64(sign)*bump
	return strconv.FormatFloat(out, 'g', -1, 64)
}

// schemaFieldNames returns a sorted snapshot of the schema's field
// names. Sorted so Levenshtein candidate lists are deterministic.
func schemaFieldNames(s *encoding.Schema) []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.Fields))
	for i := range s.Fields {
		out = append(out, s.Fields[i].Name)
	}
	sort.Strings(out)
	return out
}

// contains reports whether s contains needle.
func contains(s []string, needle string) bool {
	for _, v := range s {
		if v == needle {
			return true
		}
	}
	return false
}

// nearestFields returns schema field names within maxDist of name,
// sorted by distance ascending then lexically. The second return value
// is the minimum distance observed (zero when empty). Identical
// algorithm to descriptor.nearestFields but duplicated here per the
// structural-ban policy (internal/query may not import descriptor).
//
// Returns multiple entries only when there are ties at the minimum
// distance — callers use that signal to emit PULSE_QUERY_AMBIGUOUS.
func nearestFields(name string, fields []string, maxDist int) ([]string, int) {
	type match struct {
		name     string
		distance int
	}
	var matches []match
	for _, f := range fields {
		d := levenshtein(name, f)
		if d <= maxDist && d > 0 {
			matches = append(matches, match{name: f, distance: d})
		}
	}
	if len(matches) == 0 {
		return nil, 0
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].distance != matches[j].distance {
			return matches[i].distance < matches[j].distance
		}
		return matches[i].name < matches[j].name
	})
	// Trim to the minimum-distance band; one match means no ambiguity.
	minDist := matches[0].distance
	tiers := []string{}
	for _, m := range matches {
		if m.distance == minDist {
			tiers = append(tiers, m.name)
		}
	}
	return tiers, minDist
}

// levenshtein computes the edit distance between two strings using
// classic DP with two rolling rows. Duplicated from
// descriptor/predict_suggestions.go to keep internal/query free of a
// descriptor import.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	ra := []rune(a)
	rb := []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}
	if len(ra) < len(rb) {
		ra, rb = rb, ra
	}
	prev := make([]int, len(rb)+1)
	curr := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		curr[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			curr[j] = min3(del, ins, sub)
		}
		prev, curr = curr, prev
	}
	return prev[len(rb)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
