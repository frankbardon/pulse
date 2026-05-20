package synth

import (
	"bytes"
	mrand "math/rand/v2"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// Synth materializes a synthetic .pulse file from a Spec into the given
// path on the provided filesystem. It is the package-level entry point;
// pulse.Pulse.Synth wraps it.
func Synth(fs afero.Fs, spec *Spec, output string, opts Options) (*Result, error) {
	if fs == nil {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION, "synth: fs is required")
	}
	if err := validateSpec(spec); err != nil {
		return nil, err
	}
	rng := newRng(opts.Seed)

	schema, wfs, err := buildSchema(spec)
	if err != nil {
		return nil, err
	}

	// Stream record bytes into a separate buffer so the dictionary blocks
	// emitted with the schema are populated before being serialized.
	var recordsBuf bytes.Buffer
	rowsGenerated, rowsRejected, warnings, err := generate(spec, schema, wfs, &recordsBuf, rng)
	if err != nil {
		return nil, err
	}

	var fileBuf bytes.Buffer
	if err := encoding.WriteHeader(&fileBuf); err != nil {
		return nil, err
	}
	if err := encoding.WriteSchema(&fileBuf, schema); err != nil {
		return nil, err
	}
	if _, err := fileBuf.Write(recordsBuf.Bytes()); err != nil {
		return nil, err
	}
	if err := afero.WriteFile(fs, output, fileBuf.Bytes(), 0644); err != nil {
		return nil, errors.WrapCodedError(err, errors.CLI_OUTPUT, "writing synth output")
	}

	return &Result{
		RowsGenerated: rowsGenerated,
		RowsRejected:  rowsRejected,
		OutputPath:    output,
		Warnings:      warnings,
	}, nil
}

// SynthBytes returns the byte slice that Synth would have written, without
// touching the filesystem. Useful for round-trip tests and embedders that
// stream the output elsewhere.
func SynthBytes(spec *Spec, opts Options) ([]byte, *Result, error) {
	if err := validateSpec(spec); err != nil {
		return nil, nil, err
	}
	rng := newRng(opts.Seed)

	schema, wfs, err := buildSchema(spec)
	if err != nil {
		return nil, nil, err
	}

	var recordsBuf bytes.Buffer
	rowsGenerated, rowsRejected, warnings, err := generate(spec, schema, wfs, &recordsBuf, rng)
	if err != nil {
		return nil, nil, err
	}

	var fileBuf bytes.Buffer
	if err := encoding.WriteHeader(&fileBuf); err != nil {
		return nil, nil, err
	}
	if err := encoding.WriteSchema(&fileBuf, schema); err != nil {
		return nil, nil, err
	}
	if _, err := fileBuf.Write(recordsBuf.Bytes()); err != nil {
		return nil, nil, err
	}

	return fileBuf.Bytes(), &Result{
		RowsGenerated: rowsGenerated,
		RowsRejected:  rowsRejected,
		Warnings:      warnings,
	}, nil
}

// newRng produces a deterministic *math/rand/v2.Rand from a seed. The seed
// is split into two halves for the PCG state so seeds that differ by 1 do
// not produce strongly-correlated streams.
func newRng(seed int64) *mrand.Rand {
	hi := uint64(seed) ^ 0x9E3779B97F4A7C15
	lo := uint64(seed)*0xBF58476D1CE4E5B9 ^ 0x94D049BB133111EB
	return mrand.New(mrand.NewPCG(lo, hi))
}
