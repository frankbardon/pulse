package mcp

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"slices"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
)

// materializeSource imports the seeded data.csv as a managed cohort and
// returns the instance and the cohort path.
func materializeSource(t *testing.T) (*pulse.Pulse, string) {
	t.Helper()
	p, _ := newImportTestPulse(t)
	out, err := HandleImport(context.Background(), p, ImportIn{Source: "data.csv"})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	return p, out.Path
}

// invokeMaterialize calls pulse_materialize through the catalog's Invoke
// — the strict decode the transport runs — and returns the typed result.
func invokeMaterialize(t *testing.T, p *pulse.Pulse, args string) (MaterializeOut, error) {
	t.Helper()
	for _, td := range Tools(Config{}) {
		if td.Name != toolmeta.ToolMaterialize {
			continue
		}
		out, err := td.Invoke(context.Background(), p, json.RawMessage(args))
		if err != nil {
			return MaterializeOut{}, err
		}
		res, ok := out.(MaterializeOut)
		if !ok {
			t.Fatalf("Invoke returned %T, want MaterializeOut", out)
		}
		return res, nil
	}
	t.Fatal("pulse_materialize not in the catalog")
	return MaterializeOut{}, nil
}

func codeOf(err error) perr.Code {
	var ce *perr.CodedError
	if stderrors.As(err, &ce) {
		return ce.Code
	}
	return ""
}

// TestPulseMaterialize_WritesDerivedColumns: the tool writes the
// request's window column as an ordinary field of a new cohort under the
// data dir, and reports what it wrote.
func TestPulseMaterialize_WritesDerivedColumns(t *testing.T) {
	t.Parallel()

	p, src := materializeSource(t)
	res, err := invokeMaterialize(t, p, `{"request":{"cohort":{"filename":"`+src+`"},
		"windows":[{"type":"WIN_ROW_NUMBER","label":"rn","order_by":[{"field":"id"}]}]},
		"out":"derived.pulse"}`)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if res.Records != 3 || !slices.Equal(res.DerivedColumns, []string{"rn"}) {
		t.Fatalf("result = %+v", res)
	}
	schema, rows := readCohort(t, p, "derived.pulse")
	last := len(schema.Fields) - 1
	if schema.Fields[last].Name != "rn" {
		t.Fatalf("last field = %q, want rn", schema.Fields[last].Name)
	}
	for i, row := range rows {
		if want := []string{"1", "2", "3"}[i]; row[last] != want {
			t.Fatalf("row %d rn = %s, want %s", i, row[last], want)
		}
	}

	// An existing target needs overwrite.
	again := `{"request":{"cohort":{"filename":"` + src + `"},
		"windows":[{"type":"WIN_ROW_NUMBER","label":"rn","order_by":[{"field":"id"}]}]},
		"out":"derived.pulse"`
	if _, err := invokeMaterialize(t, p, again+`}`); codeOf(err) != perr.SERVICE_VALIDATION {
		t.Fatalf("existing out: err %v, want SERVICE_VALIDATION", err)
	}
	if _, err := invokeMaterialize(t, p, again+`,"overwrite":true}`); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
}

// TestPulseMaterialize_RefusalsAreCoded: every refusal reaches the agent
// with its own code — a request spread at the root (or a misspelled
// argument), an unknown request slot, an unsupported slot — and a
// missing argument is refused before the facade runs.
func TestPulseMaterialize_RefusalsAreCoded(t *testing.T) {
	t.Parallel()

	p, src := materializeSource(t)
	cohort := `{"filename":"` + src + `"}`
	win := `[{"type":"WIN_ROW_NUMBER","label":"rn","order_by":[{"field":"id"}]}]`

	_, err := invokeMaterialize(t, p, `{"cohort":`+cohort+`,"windows":`+win+`,"out":"x.pulse"}`)
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_REQUEST_UNKNOWN_FIELD {
		t.Fatalf("request spread at the root: err %v, want PULSE_REQUEST_UNKNOWN_FIELD", err)
	}
	if got, _ := ce.Details["unknown_keys"].([]string); !slices.Equal(got, []string{"cohort", "windows"}) {
		t.Fatalf("unknown_keys = %v", ce.Details["unknown_keys"])
	}

	_, err = invokeMaterialize(t, p, `{"request":{"cohort":`+cohort+`,"windows":`+win+`},"out":"x.pulse","overwite":true}`)
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_REQUEST_UNKNOWN_FIELD {
		t.Fatalf("misspelled argument: err %v", err)
	}
	if sug, _ := ce.Details["suggestions"].(map[string]any); sug["overwite"] != "overwrite" {
		t.Fatalf("suggestions = %v, want overwite → overwrite", ce.Details["suggestions"])
	}

	_, err = invokeMaterialize(t, p, `{"request":{"cohort":`+cohort+`,"windowz":`+win+`},"out":"x.pulse"}`)
	if codeOf(err) != perr.PULSE_REQUEST_UNKNOWN_FIELD {
		t.Fatalf("unknown request slot: err %v", err)
	}

	_, err = invokeMaterialize(t, p, `{"request":{"cohort":`+cohort+`,"aggregations":[{"type":"AGG_COUNT","field":"id"}]},"out":"x.pulse"}`)
	if codeOf(err) != perr.PULSE_MATERIALIZE_SLOT_UNSUPPORTED {
		t.Fatalf("aggregations: err %v, want PULSE_MATERIALIZE_SLOT_UNSUPPORTED", err)
	}

	if _, err := invokeMaterialize(t, p, `{"out":"x.pulse"}`); err == nil {
		t.Fatal("missing request accepted")
	}
	if _, err := invokeMaterialize(t, p, `{"request":{"cohort":`+cohort+`,"windows":`+win+`}}`); err == nil {
		t.Fatal("missing out accepted")
	}
	if _, err := p.Fs().Stat("x.pulse"); err == nil {
		t.Fatal("a refused call wrote x.pulse")
	}
}
