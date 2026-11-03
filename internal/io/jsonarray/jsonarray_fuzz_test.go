package jsonarray

import (
	"context"
	"testing"

	pio "github.com/frankbardon/pulse/internal/io"
	"github.com/frankbardon/pulse/internal/testutil/fuzzcontract"
	"github.com/spf13/afero"
)

// jsonArrayFuzzSeeds is one document per reader and import branch: an
// inferred import of every value family, a null and a late key, a list
// cell, a malformed element after a good one, a document that is not
// an array, a non-object element, a nested object and empty forms.
var jsonArrayFuzzSeeds = []string{
	`[{"name":"alice","age":30,"score":95.5,"ok":true},{"name":"bob","age":25,"score":88.0,"ok":false}]`,
	`[{"a":1,"b":null},{"a":2,"c":"late"}]`,
	`[{"tags":["x","y"],"v":[1,2]},{"tags":[],"v":[3,null]}]`,
	`[{"d":"2024-01-02","ts":"2024-01-02T03:04:05Z"}]`,
	`[{"a":1},{"a":`,
	`[{"a":`,
	`{"a":1}`,
	`[1,2,3]`,
	`[{"a":{"b":1}}]`,
	`[]`,
	``,
	`[{}]`,
	`[{"a":1}] trailing`,
}

// FuzzJSONArrayImport holds the JSON array importer — schema inference
// plus the record pass an embedder runs through pulse.Import
// (ImportJob.Run into a hermetic fs), the plain ImportJob.Predict, and,
// when the inferred import succeeds, the explicit-schema re-import that
// arms the late-key refusal — to the fuzz contract: no panic, a
// bounded run, and every error a registered *errors.CodedError.
func FuzzJSONArrayImport(f *testing.F) {
	for _, s := range jsonArrayFuzzSeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		fuzzcontract.Run(t, in, func(in []byte) error {
			return jsonArrayFuzzImport(in)
		})
	})
}

func jsonArrayFuzzImport(in []byte) error {
	ctx := context.Background()
	if _, err := pio.NewImportJob(NewReaderFromBytes(in), "p.pulse").Predict(ctx); err != nil {
		return err
	}
	job := pio.NewImportJob(NewReaderFromBytes(in), "in.pulse")
	job.FS = afero.NewMemMapFs()
	rep, err := job.Run(ctx)
	if err != nil || rep == nil || rep.Schema == nil || len(rep.Schema.Fields) == 0 {
		return err
	}
	again := pio.NewImportJob(NewReaderFromBytes(in), "ex.pulse")
	again.FS = job.FS
	again.Schema = rep.Schema
	_, err = again.Run(ctx)
	return err
}
