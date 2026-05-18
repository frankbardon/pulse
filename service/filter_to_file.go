package service

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// FilterToFile reads the single-file .pulse cohort at src, evaluates
// filterExpr against every record using FILTER_EXPRESSION semantics,
// and writes a new single-file .pulse cohort at dst containing only
// records that pass. The header and schema bytes are copied byte-
// for-byte from src — only the record payload differs. Custom expr
// functions and lookup tables registered via pulse.Options.Extensions
// are honored.
//
// Shard archives are not supported in this entry. Open one shard via
// the `archive.pulse#shard.pulse` anchor or use `pulse shard extract`
// first. Returns the number of records written.
func (s *Service) FilterToFile(ctx context.Context, src, dst, filterExpr string) (int64, error) {
	if src == "" {
		return 0, errors.NewCodedError(errors.SERVICE_VALIDATION,
			"filter_to_file requires a non-empty input path")
	}
	if dst == "" {
		return 0, errors.NewCodedError(errors.SERVICE_VALIDATION,
			"filter_to_file requires a non-empty output path")
	}
	if filterExpr == "" {
		return 0, errors.NewCodedError(errors.SERVICE_VALIDATION,
			"filter_to_file requires a non-empty filter expression")
	}

	fsys := s.fs.Fs()
	data, err := afero.ReadFile(fsys, src)
	if err != nil {
		return 0, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("opening cohort file: %s", src))
	}
	if len(data) >= 4 && data[0] == 'P' && data[1] == 'K' && data[2] == 0x03 && data[3] == 0x04 {
		return 0, errors.NewCodedError(errors.SERVICE_VALIDATION,
			"filter_to_file does not support shard archives; pass an anchored single shard or extract first")
	}

	br := bytes.NewReader(data)
	if err := encoding.ReadHeader(br); err != nil {
		return 0, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			fmt.Sprintf("invalid pulse file: %s", src))
	}
	schema, err := encoding.ReadSchema(br)
	if err != nil {
		return 0, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			fmt.Sprintf("reading schema from: %s", src))
	}
	headerSchemaEnd := int64(len(data)) - int64(br.Len())

	filterFuncs, err := processing.BuildFilters(
		[]*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: filterExpr}},
		schema,
		s.extensions,
	)
	if err != nil {
		return 0, err
	}
	filterFn := filterFuncs[0]

	out := bytes.NewBuffer(make([]byte, 0, len(data)))
	out.Write(data[:headerSchemaEnd])

	values := make(map[string]float64, len(schema.Fields))
	nulls := make(map[string]bool, len(schema.Fields))
	wide := make(map[string]any, len(schema.Fields))
	rr := encoding.NewRecordReader(br, schema)

	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		posStart := int64(len(data)) - int64(br.Len())
		err := rr.ReadRecordWithWide(values, nulls, wide)
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}
		posEnd := int64(len(data)) - int64(br.Len())

		rec := processing.NewRecordWithWide(schema, values, nulls, wide)
		keep, ferr := filterFn(rec)
		if ferr != nil {
			return 0, ferr
		}
		if !keep {
			continue
		}
		out.Write(data[posStart:posEnd])
		written++
	}

	if err := afero.WriteFile(fsys, dst, out.Bytes(), 0644); err != nil {
		return 0, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("writing filtered cohort to: %s", dst))
	}
	return written, nil
}
