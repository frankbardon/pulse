package imports

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// TestManager_Open_WidthPromotion: a managed import (and so pulse_import,
// whose output is this Result) keeps every row of a source whose parent
// names outgrow the categorical_u8 the inference sample picks, and
// reports the promotion as width_warnings.
func TestManager_Open_WidthPromotion(t *testing.T) {
	m, afs, _ := newTestManager(t)
	var b strings.Builder
	b.WriteString("line_id,parent_name,qty\n")
	for i := 0; i < 4000; i++ {
		fmt.Fprintf(&b, "%d,parent-%04d,%d\n", i, i/10, i%9)
	}
	if err := afero.WriteFile(afs, "lines.csv", []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := m.Open(context.Background(), Spec{SourcePath: "lines.csv"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if res.RowsImported != 4000 {
		t.Fatalf("RowsImported = %d, want all 4000", res.RowsImported)
	}
	var parent encoding.FieldType
	for _, f := range res.Schema.Fields {
		if f.Name == "parent_name" {
			parent = f.Type
		}
	}
	if parent != encoding.FieldTypeCategoricalU16 {
		t.Errorf("parent_name = %s, want categorical_u16", parent)
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		WidthWarnings []struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"width_warnings"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	fields := map[string]string{}
	for _, w := range wire.WidthWarnings {
		if w.Code != string(perr.PULSE_IMPORT_WIDTH_PROMOTED) {
			t.Errorf("width warning code %s", w.Code)
		}
		fields[fmt.Sprint(w.Details["field"])] = fmt.Sprint(w.Details["to"])
	}
	if len(fields) != 1 || fields["parent_name"] != "categorical_u16" {
		t.Errorf("width_warnings on the wire = %s, want parent_name → categorical_u16 only", raw)
	}
}
