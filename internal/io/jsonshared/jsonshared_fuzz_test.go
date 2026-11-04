package jsonshared_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	pio "github.com/frankbardon/pulse/internal/io"
	"github.com/frankbardon/pulse/internal/io/jsonarray"
	"github.com/frankbardon/pulse/internal/io/jsonshared"
	"github.com/frankbardon/pulse/internal/io/ndjson"
	"github.com/frankbardon/pulse/internal/iocore"
	"github.com/frankbardon/pulse/internal/testutil/fuzzcontract"
	"github.com/spf13/afero"
)

// jsonSharedFuzzSeeds are documents whose cells walk every helper
// branch: numeric, null-bearing, mixed, ragged and nested lists, a
// non-list under a list column, scalars of every JSON type, and a key
// first seen after the first object.
var jsonSharedFuzzSeeds = []string{
	"{\"v\":[1,2],\"w\":[0.5,1.5]}\n{\"v\":[3,null],\"w\":null}\n",
	"[{\"v\":[1,2],\"w\":[0.5,1.5]},{\"v\":[3,4],\"w\":[1e40,2]}]",
	"{\"v\":[1,\"2\"],\"w\":[true,1]}\n",
	"[{\"v\":[1,2]},{\"v\":[1,2,3]}]",
	"{\"v\":3,\"w\":\"x\"}\n{\"v\":[1e400,2],\"w\":[]}\n",
	"[{\"v\":[[1],[2]]}]",
	"{\"v\":[1,2],\"late\":1}\n{\"v\":[3,4],\"extra\":2}\n",
	"[{\"v\":null},{\"v\":[null,null]}]",
	"{\"s\":\"\",\"b\":false,\"n\":-0,\"x\":123456789012345678901234567890}\n",
}

// FuzzJSONSharedImport holds the cell helpers the NDJSON and JSON
// array importers share — the typed list read behind a native vector
// column (ListCellOf through the import's vector fold), the
// pipe-joined text and coercion of every scalar and list
// (ValueToString, CoerceValue, CoerceValueExplicitNull,
// ListColumns.Value) and the late-key refusal (LateKeys) — to the fuzz
// contract, through both readers: every input imports as an NDJSON
// and as a JSON array source with its first keys declared native list
// vectors (ImportJob.Run into a hermetic fs). No panic, a bounded run,
// and every error a registered *errors.CodedError.
func FuzzJSONSharedImport(f *testing.F) {
	for _, s := range jsonSharedFuzzSeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		fuzzcontract.Run(t, in, func(in []byte) error {
			jsonSharedFuzzCells(in)
			if err := jsonSharedFuzzVectors(ndjson.NewReaderFromBytes(in), func() pio.Reader { return ndjson.NewReaderFromBytes(in) }); err != nil {
				return err
			}
			return jsonSharedFuzzVectors(jsonarray.NewReaderFromBytes(in), func() pio.Reader { return jsonarray.NewReaderFromBytes(in) })
		})
	})
}

// jsonSharedFuzzVectors declares the source's first key a native f64
// list vector and its second an f32 one, then imports.
func jsonSharedFuzzVectors(probe interface{ ReadHeader() ([]string, error) }, source func() pio.Reader) error {
	header, err := probe.ReadHeader()
	if err != nil || len(header) == 0 {
		// The reader's own fault surfaces through the import below.
		header = nil
	}
	job := pio.NewImportJob(source(), "v.pulse")
	job.FS = afero.NewMemMapFs()
	if len(header) > 0 {
		job.Vectors = []pio.VectorDecl{{Name: header[0]}}
	}
	if len(header) > 1 && header[1] != header[0] {
		job.Vectors = append(job.Vectors, pio.VectorDecl{Name: header[1], Element: "f32"})
	}
	_, err = job.Run(context.Background())
	return err
}

// jsonSharedFuzzCells runs every decoded value of every line (and of
// the whole input) through the pure helpers; they return no error, so
// only a panic can fail them.
func jsonSharedFuzzCells(in []byte) {
	lists := jsonshared.NewListColumns([]iocore.ListColumn{{Index: 0}})
	visit := func(v any) {
		_ = jsonshared.ValueToString(v)
		_ = jsonshared.CoerceValue(v)
		_ = jsonshared.CoerceValueExplicitNull(v)
		_ = jsonshared.CoerceValue(jsonshared.ValueToString(v))
		var cell iocore.ListCell
		jsonshared.ListCellOf(v, true, &cell)
		if cell.State == iocore.ListValues {
			_, _ = lists.Value(0, append([]float64(nil), cell.Values...))
		}
	}
	for _, line := range append(bytes.Split(in, []byte("\n")), in) {
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.UseNumber()
		var v any
		if dec.Decode(&v) != nil {
			continue
		}
		visit(v)
		var objs []map[string]any
		switch x := v.(type) {
		case map[string]any:
			objs = []map[string]any{x}
		case []any:
			for _, el := range x {
				visit(el)
				if o, ok := el.(map[string]any); ok {
					objs = append(objs, o)
				}
			}
		}
		var late jsonshared.LateKeys
		late.Set(true)
		var header []string
		for i, o := range objs {
			keys := make([]string, 0, len(o))
			for k, el := range o {
				keys = append(keys, k)
				visit(el)
			}
			if i == 0 {
				header = keys
			}
			_ = late.Check(keys, header, i+1, map[string]any{"line": i + 1})
		}
	}
}
