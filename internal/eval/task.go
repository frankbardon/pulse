package eval

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	idesc "github.com/frankbardon/pulse/internal/descriptor"
)

//go:embed tasks/*.json
var taskFiles embed.FS

// Task is one evaluation question with its reference submission and
// the assertions any answer must satisfy (package doc: Task files).
type Task struct {
	ID         string      `json:"id"`
	Intent     string      `json:"intent"`
	Question   string      `json:"question"`
	Cohorts    []string    `json:"cohorts,omitempty"`
	Files      []string    `json:"files,omitempty"`
	Indexes    []Index     `json:"indexes,omitempty"`
	Reference  Submission  `json:"reference"`
	Assertions []Assertion `json:"assertions"`
}

// Index is a point-lookup index a task's data directory needs.
type Index struct {
	Cohort string   `json:"cohort"`
	Fields []string `json:"fields"`
}

// Submission is a tool call: a pulse MCP tool name (or "synth") and
// its arguments.
type Submission struct {
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

// Metric resolves the output key of a request operator: the label of
// an operator of one of Types over Field.
type Metric struct {
	Types []string `json:"types"`
	Field string   `json:"field"`
}

// Assertion is one check on a run (package doc: Assertions).
type Assertion struct {
	Kind string `json:"kind"`

	// Scope.
	Section string   `json:"section,omitempty"`
	Types   []string `json:"types,omitempty"`
	InRow   []string `json:"in_row,omitempty"`
	Key     string   `json:"key,omitempty"`
	Metric  *Metric  `json:"metric,omitempty"`

	// operator.
	AnyOf []string `json:"any_of,omitempty"`
	// value.
	Near *float64 `json:"near,omitempty"`
	Tol  float64  `json:"tol,omitempty"`
	Min  *float64 `json:"min,omitempty"`
	Max  *float64 `json:"max,omitempty"`
	// sign.
	Sign string `json:"sign,omitempty"`
	// order.
	High string `json:"high,omitempty"`
	Low  string `json:"low,omitempty"`
	// top.
	Value  string `json:"value,omitempty"`
	Lowest bool   `json:"lowest,omitempty"`
	// rows.
	Present  []string `json:"present,omitempty"`
	Count    *int     `json:"count,omitempty"`
	MinCount *int     `json:"min_count,omitempty"`
	MaxCount *int     `json:"max_count,omitempty"`
	// row.
	Match map[string]any `json:"match,omitempty"`
	// warning / error.
	Code string `json:"code,omitempty"`
	// cohort.
	Rows       *int64            `json:"rows,omitempty"`
	Fields     []string          `json:"fields,omitempty"`
	FieldTypes map[string]string `json:"field_types,omitempty"`
	// any.
	Assertions []Assertion `json:"assertions,omitempty"`
}

// assertionKinds is the closed set of assertion kinds.
var assertionKinds = map[string]bool{
	"operator": true, "value": true, "sign": true, "order": true, "top": true,
	"rows": true, "row": true, "warning": true, "error": true, "cohort": true, "any": true,
}

// Tasks returns every embedded task, sorted by ID.
func Tasks() ([]*Task, error) {
	entries, err := taskFiles.ReadDir("tasks")
	if err != nil {
		return nil, err
	}
	var out []*Task
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".json" {
			continue
		}
		raw, err := taskFiles.ReadFile("tasks/" + e.Name())
		if err != nil {
			return nil, err
		}
		t, err := ParseTask(raw)
		if err != nil {
			return nil, fmt.Errorf("tasks/%s: %w", e.Name(), err)
		}
		if want := strings.TrimSuffix(e.Name(), ".json"); t.ID != want {
			return nil, fmt.Errorf("tasks/%s: id %q, want the file stem %q", e.Name(), t.ID, want)
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ParseTask strictly decodes one task file and validates it.
func ParseTask(raw []byte) (*Task, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var t Task
	if err := dec.Decode(&t); err != nil {
		return nil, err
	}
	if err := t.validate(); err != nil {
		return nil, err
	}
	return &t, nil
}

func (t *Task) validate() error {
	switch {
	case t.ID == "":
		return fmt.Errorf("missing id")
	case !idesc.IsIntent(t.Intent):
		return fmt.Errorf("intent %q is not an intent-taxonomy ID", t.Intent)
	case strings.TrimSpace(t.Question) == "":
		return fmt.Errorf("missing question")
	case t.Reference.Tool == "":
		return fmt.Errorf("reference names no tool")
	case len(t.Assertions) == 0:
		return fmt.Errorf("has no assertions")
	}
	return validateAssertions(t.Assertions)
}

func validateAssertions(as []Assertion) error {
	for i, a := range as {
		if !assertionKinds[a.Kind] {
			return fmt.Errorf("assertions[%d]: unknown kind %q", i, a.Kind)
		}
		if a.Kind == "any" {
			if len(a.Assertions) == 0 {
				return fmt.Errorf("assertions[%d]: any needs nested assertions", i)
			}
			if err := validateAssertions(a.Assertions); err != nil {
				return fmt.Errorf("assertions[%d].%w", i, err)
			}
		}
	}
	return nil
}
