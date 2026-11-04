package ndjson

import (
	"context"
	"testing"

	pio "github.com/frankbardon/pulse/internal/io"
	"github.com/frankbardon/pulse/internal/testutil/fuzzcontract"
	"github.com/spf13/afero"
)

// ndjsonFuzzSeeds is one document per reader and import branch: an
// inferred import of every value family, a null and a late key, a list
// cell, a malformed line after a good one, a malformed first line, a
// nested object, an empty document and blank lines.
var ndjsonFuzzSeeds = []string{
	"{\"name\":\"alice\",\"age\":30,\"score\":95.5,\"ok\":true}\n{\"name\":\"bob\",\"age\":25,\"score\":88.0,\"ok\":false}\n",
	"{\"a\":1,\"b\":null}\n{\"a\":2,\"c\":\"late\"}\n",
	"{\"tags\":[\"x\",\"y\"],\"v\":[1,2]}\n{\"tags\":[],\"v\":[3,null]}\n",
	"{\"d\":\"2024-01-02\",\"ts\":\"2024-01-02T03:04:05Z\"}\n",
	"{\"a\":1}\n{\"a\":\n",
	"{\"a\":\n",
	"{\"a\":{\"b\":1}}\n",
	"[1,2,3]\n",
	"",
	"\n\n  \n{\"a\":\"\"}\n",
	"{\"a\":1,\"a\":2}\n",
	"{\"\":1}\n",
}

// FuzzNDJSONImport holds the NDJSON importer — schema inference plus
// the record pass an embedder runs through pulse.Import (ImportJob.Run
// into a hermetic fs), the plain ImportJob.Predict, and, when the
// inferred import succeeds, the explicit-schema re-import that arms
// the late-key refusal — to the fuzz contract: no panic, a bounded
// run, and every error a registered *errors.CodedError.
func FuzzNDJSONImport(f *testing.F) {
	for _, s := range ndjsonFuzzSeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		fuzzcontract.Run(t, in, func(in []byte) error {
			return ndjsonFuzzImport(in)
		})
	})
}

func ndjsonFuzzImport(in []byte) error {
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
