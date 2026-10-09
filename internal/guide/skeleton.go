package guide

import (
	"bytes"
	"encoding/json"
	"sort"
)

// placeholder spells the unbound value for wire key k.
func placeholder(k string) string { return "<" + k + ">" }

// optionalSlots adds a slot a test does not REQUIRE but needs to answer
// the intent: TEST_T runs one-sample unless split_by is set, and a
// group comparison is the two-sample form.
var optionalSlots = map[[2]string][]string{
	{"TEST_T", "compare_groups"}: {"SplitBy"},
}

// filtererShape is the value slot each built-in filterer reads beside
// (or instead of) its field: the filterer catalog declares no params,
// so the shape is written down here, from the example library.
var filtererShape = map[string]struct {
	noField bool
	key     string // "" = the field alone
	inParam bool   // key is a params member, not a top-level key
}{
	"FILTER_EXPRESSION":        {noField: true, key: "expression"},
	"FILTER_TRUE":              {},
	"FILTER_FALSE":             {},
	"FILTER_DATE_RANGES":       {key: "ranges", inParam: true},
	"FILTER_INCLUDE":           {key: "values"},
	"FILTER_EXCLUDE":           {key: "values"},
	"FILTER_NULL":              {key: "values"},
	"FILTER_RANGE":             {key: "values"},
	"FILTER_SET_CONTAINS_ALL":  {key: "values"},
	"FILTER_SET_CONTAINS_ANY":  {key: "values"},
	"FILTER_SET_CONTAINS_NONE": {key: "values"},
	"FILTER_SET_EQUALS":        {key: "values"},
}

// listKeys are the top-level slot keys whose value is a list.
var listKeys = map[string]bool{"values": true, "predictors": true, "fields": true}

// draft is one built skeleton.
type draft struct {
	request      json.RawMessage
	placeholders []string
}

// skeleton builds the unbound draft request for op under intent: the
// cohort plus one entry in op's slot, every caller-supplied value a
// placeholder named after its wire key. ok is false when the operator
// declares a required slot the mapping table does not know (a gap
// TestRequireKeysMatchWire catches first).
func skeleton(op opInfo, intent string) (draft, bool) {
	var ph []string
	seen := map[string]bool{}
	note := func(k string) {
		if !seen[k] {
			seen[k] = true
			ph = append(ph, k)
		}
	}
	str := func(k string) string { note(k); return placeholder(k) }
	list := func(k string) []string { note(k); return []string{placeholder(k)} }

	entry := map[string]any{"type": op.name}
	params := map[string]any{}
	putParam := func(p param) {
		var v any = str(p.name)
		if p.list {
			v = list(p.name)
		}
		if op.category == catRegression || topLevelParams[op.category][p.name] {
			entry[p.name] = v
			return
		}
		params[p.name] = v
	}

	switch op.category {
	case catTest, catPostTest:
		req := append([]string(nil), op.requires...)
		req = append(req, optionalSlots[[2]string{op.name, intent}]...)
		for _, goName := range req {
			k, ok := requireKey[goName]
			if !ok {
				return draft{}, false
			}
			if k == "order_by" {
				note(k)
				entry[k] = []map[string]any{{"field": placeholder(k)}}
				continue
			}
			entry[k] = str(k)
		}
	case catRegression:
		// target / predictors are declared required params (top-level).
	case catMatrix:
		entry["fields"] = list("fields")
	case catFilterer:
		shape, known := filtererShape[op.name]
		if !shape.noField {
			entry["field"] = str("field")
		}
		if known && shape.key != "" {
			var v any = str(shape.key)
			if listKeys[shape.key] || shape.inParam {
				v = list(shape.key)
			}
			if shape.inParam {
				params[shape.key] = v
			} else {
				entry[shape.key] = v
			}
		}
	case catWindow:
		if !op.ignoresField {
			entry["field"] = str("field")
		}
		note("order_by")
		entry["order_by"] = []map[string]any{{"field": placeholder("order_by")}}
	default: // aggregator, attribute, grouper, feature
		if !op.ignoresField {
			entry["field"] = str("field")
		}
	}
	for _, p := range op.params {
		putParam(p)
	}
	if len(params) > 0 {
		entry["params"] = params
	}

	body := map[string]any{
		"cohort":             map[string]any{"filename": placeholder("cohort")},
		slotKey[op.category]: []any{entry},
	}
	ph = append([]string{"cohort"}, ph...)
	// Placeholders are "<key>": keep the angle brackets literal rather
	// than HTML-escaped, so the draft reads as written.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(body); err != nil {
		return draft{}, false
	}
	return draft{request: bytes.TrimRight(buf.Bytes(), "\n"), placeholders: sortedUnique(ph)}, true
}

func sortedUnique(in []string) []string {
	m := map[string]bool{}
	var out []string
	for _, s := range in {
		if !m[s] {
			m[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
