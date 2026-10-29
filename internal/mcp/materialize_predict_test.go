package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// TestPulseMaterialize_Predict: predict: true answers under `predict`
// with the output columns, writes nothing and needs no out; a refused
// request is a verdict carrying the run's code in errors, not a tool
// error; the predict key is part of the strict argument set.
func TestPulseMaterialize_Predict(t *testing.T) {
	t.Parallel()

	afs := afero.NewMemMapFs()
	if err := afero.WriteFile(afs, "data.csv", []byte("id,name,amount\n1,Alice,10.5\n2,Bob,20.0\n3,Carol,30.75\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: afs})
	if err != nil {
		t.Fatal(err)
	}
	imp, err := HandleImport(context.Background(), p, ImportIn{Source: "data.csv"})
	if err != nil {
		t.Fatal(err)
	}
	src := imp.Path
	res, err := invokeMaterialize(t, p, `{"request":{"cohort":{"filename":"`+src+`"},
		"features":[{"type":"FEAT_LOG","field":"amount"}],
		"output":{"columns":[{"from":"LOG_amount","as":"log_amount","nullable":false},{"from":"id"}]}},
		"predict":true}`)
	if err != nil {
		t.Fatalf("predict: %v", err)
	}
	if res.MaterializeResult != nil || res.Predict == nil || !res.Predict.Valid || len(res.Predict.Errors) != 0 {
		t.Fatalf("result = %+v", res)
	}
	cols := res.Predict.Columns
	if len(cols) != 2 || cols[0].Name != "log_amount" || cols[0].Type != "f64" || cols[0].Nullable || cols[1].From != "id" {
		t.Errorf("columns = %+v", cols)
	}
	if len(res.Predict.Risks) != 1 || res.Predict.Risks[0].Code != string(perr.PULSE_MATERIALIZE_OUTPUT_NULL) {
		t.Errorf("risks = %+v, want the nullable: false over a derived column", res.Predict.Risks)
	}
	if ok, _ := afero.Exists(afs, "never.pulse"); ok {
		t.Error("predict wrote a cohort")
	}

	res, err = invokeMaterialize(t, p, `{"request":{"cohort":{"filename":"`+src+`"},
		"features":[{"type":"FEAT_LOG","field":"amount"}],"output":{"exclude":["nope"]}},"predict":true,"out":"never.pulse"}`)
	if err != nil {
		t.Fatalf("refused predict is a tool error: %v", err)
	}
	if res.Predict == nil || res.Predict.Valid || len(res.Predict.Errors) == 0 || res.Predict.Errors[0].Code != string(perr.PULSE_MATERIALIZE_OUTPUT_UNKNOWN_COLUMN) {
		t.Fatalf("refused predict = %+v", res)
	}
	if ok, _ := afero.Exists(afs, "never.pulse"); ok {
		t.Error("predict wrote a cohort at out")
	}
	if _, err := invokeMaterialize(t, p, `{"request":{"cohort":{"filename":"`+src+`"}},"predcit":true}`); codeOf(err) != perr.PULSE_REQUEST_UNKNOWN_FIELD {
		t.Errorf("misspelled predict: err %v, want PULSE_REQUEST_UNKNOWN_FIELD", err)
	}
}

// TestPulseMaterialize_RunOutputByteIdentical: a run's answer embeds the
// MaterializeResult, so its wire form is the result's own, key for key.
func TestPulseMaterialize_RunOutputByteIdentical(t *testing.T) {
	t.Parallel()

	res := &pulse.MaterializeResult{Target: "out.pulse", Records: 3, DerivedColumns: []string{"LOG_amount"},
		Warnings: []*perr.CodedError{perr.NewCodedError(perr.PULSE_FIELD_DESCRIPTION_LOW_QUALITY, "weak")}}
	want, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(MaterializeOut{MaterializeResult: res})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("run answer\n%s\nwant\n%s", got, want)
	}
}
