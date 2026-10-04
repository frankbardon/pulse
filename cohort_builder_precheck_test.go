package pulse

import (
	"context"
	stderrors "errors"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
)

// precheckField is one column of a precheck schema: layout is
// recomputed by the builder, so only name, type, nullability and the
// pre-seeded labels matter.
type precheckField struct {
	name     string
	typ      encoding.FieldType
	nullable bool
	labels   []string
	desc     string
}

func precheckSchema(fs ...precheckField) encoding.Schema {
	out := encoding.Schema{Fields: make([]encoding.Field, len(fs))}
	for i, f := range fs {
		desc := f.desc
		if desc == "" {
			desc = "Precheck column " + f.name + " for the anchored append."
		}
		out.Fields[i] = encoding.Field{Name: f.name, Type: f.typ, Nullable: f.nullable, Description: desc, CsvColumnIdx: i}
		if len(f.labels) > 0 {
			d := encoding.NewDictionary()
			for _, l := range f.labels {
				_, _ = d.Add(l)
			}
			out.Fields[i].Dictionary = d
		}
	}
	return out
}

// precheckBase is the archive's schema in every precheck case.
func precheckBase() []precheckField {
	return []precheckField{
		{name: "id", typ: encoding.FieldTypeU32},
		{name: "kind", typ: encoding.FieldTypeCategoricalU8},
		{name: "tags", typ: encoding.FieldTypeSetU8},
		{name: "score", typ: encoding.FieldTypeF64, nullable: true},
	}
}

func precheckRows() []CohortRow {
	rows := make([]CohortRow, 6)
	for i := range rows {
		rows[i] = CohortRow{uint64(i), []string{"a", "b"}[i%2], []string{"x"}, float64(i) / 2}
	}
	return rows
}

func labelsN(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s%03d", prefix, i)
	}
	return out
}

// TestCohortBuilder_AppendPrecheckParity: the cohesion pre-check an
// anchored NewCohortBuilder runs agrees with AddShard on every case —
// each structural mismatch AddShard refuses is refused at construction
// with AddShard's own code, message and details, and every case
// AddShard accepts (a set rung to widen, a new pre-seeded label to
// union, a grouped archive to re-encode into, a diverging description)
// is accepted at construction. The reference outcome is AddShard of a
// zero-row shard built with the same schema; a refusal leaves the
// archive byte-identical and nothing else on the filesystem.
func TestCohortBuilder_AppendPrecheckParity(t *testing.T) {
	with := func(mut func(fs []precheckField) []precheckField) []precheckField {
		return mut(precheckBase())
	}
	for _, tc := range []struct {
		name    string
		archive []precheckField
		grouped bool
		builder []precheckField
		want    errors.Code // "" = accepted
	}{
		{name: "identical", builder: precheckBase()},
		{name: "field count", builder: with(func(fs []precheckField) []precheckField { return fs[:3] }),
			want: errors.PULSE_SHARD_SCHEMA_MISMATCH},
		{name: "field name", builder: with(func(fs []precheckField) []precheckField { fs[3].name = "points"; return fs }),
			want: errors.PULSE_SHARD_SCHEMA_MISMATCH},
		{name: "field order", builder: with(func(fs []precheckField) []precheckField { fs[0], fs[3] = fs[3], fs[0]; return fs }),
			want: errors.PULSE_SHARD_SCHEMA_MISMATCH},
		{name: "numeric type", builder: with(func(fs []precheckField) []precheckField { fs[0].typ = encoding.FieldTypeU16; return fs }),
			want: errors.PULSE_SHARD_SCHEMA_MISMATCH},
		{name: "categorical width", builder: with(func(fs []precheckField) []precheckField { fs[1].typ = encoding.FieldTypeCategoricalU16; return fs }),
			want: errors.PULSE_SHARD_SCHEMA_MISMATCH},
		{name: "set to non-set", builder: with(func(fs []precheckField) []precheckField { fs[2].typ = encoding.FieldTypeCategoricalU8; return fs }),
			want: errors.PULSE_SHARD_SCHEMA_MISMATCH},
		{name: "pre-seeded union overflow",
			archive: with(func(fs []precheckField) []precheckField { fs[1].labels = labelsN("old", 250); return fs }),
			builder: with(func(fs []precheckField) []precheckField { fs[1].labels = labelsN("new", 10); return fs }),
			want:    errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW},
		{name: "wider set rung", builder: with(func(fs []precheckField) []precheckField { fs[2].typ = encoding.FieldTypeSetU16; return fs })},
		{name: "narrower set rung",
			archive: with(func(fs []precheckField) []precheckField { fs[2].typ = encoding.FieldTypeSetU16; return fs }),
			builder: precheckBase()},
		{name: "new pre-seeded labels", builder: with(func(fs []precheckField) []precheckField {
			fs[1].labels = []string{"c", "d"}
			fs[2].labels = []string{"y"}
			return fs
		})},
		{name: "diverging description", builder: with(func(fs []precheckField) []precheckField {
			fs[3].desc = "A different score description, still long enough."
			return fs
		})},
		{name: "grouped archive", grouped: true, builder: precheckBase()},
		{name: "grouped archive, field name", grouped: true,
			builder: with(func(fs []precheckField) []precheckField { fs[3].name = "points"; return fs }),
			want:    errors.PULSE_SHARD_SCHEMA_MISMATCH},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archFields := tc.archive
			if archFields == nil {
				archFields = precheckBase()
			}
			opts := CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: 3}}
			if tc.grouped {
				opts.Groups = []pio.GroupDecl{{Key: []string{"kind"}, Members: []string{"score"}}}
				opts.RatioFloor = 1
			}
			rows := precheckRows()
			if tc.grouped {
				for i := range rows {
					rows[i][3] = float64(i % 2)
				}
			}
			p, fsys := memBuilderEngine(t)
			ctx := context.Background()
			buildCohort(t, p, "arch.pulse", precheckSchema(archFields...), opts, rows)
			if tc.grouped {
				if r := openReader(t, p, "arch.pulse"); !r.Schema().HasGroups() {
					t.Fatal("setup: the grouped archive carries no group")
				}
			}
			before := readFile(t, fsys, "arch.pulse")

			// The pre-check, at construction.
			b, preErr := p.NewCohortBuilder(ctx, "arch.pulse#drop.pulse", precheckSchema(tc.builder...), CohortBuilderOptions{})
			if preErr == nil {
				if err := b.Abort(); err != nil {
					t.Fatal(err)
				}
			} else {
				if string(readFile(t, fsys, "arch.pulse")) != string(before) {
					t.Fatal("a pre-check refusal rewrote the archive")
				}
				assertOnlyArchive(t, fsys, "arch.pulse")
			}

			// The reference: AddShard of a zero-row shard with the same schema.
			buildCohort(t, p, "drop.pulse", precheckSchema(tc.builder...), CohortBuilderOptions{}, nil)
			_, addErr := p.AddShard(ctx, "arch.pulse", "drop.pulse")

			if tc.want == "" {
				if preErr != nil || addErr != nil {
					t.Fatalf("want accepted: pre-check = %v, AddShard = %v", preErr, addErr)
				}
				return
			}
			var pre, add *errors.CodedError
			if !stderrors.As(preErr, &pre) || !stderrors.As(addErr, &add) {
				t.Fatalf("want %s from both: pre-check = %v, AddShard = %v", tc.want, preErr, addErr)
			}
			if pre.Code != tc.want || add.Code != tc.want {
				t.Fatalf("codes: pre-check %s, AddShard %s, want %s", pre.Code, add.Code, tc.want)
			}
			if pre.Message != add.Message || fmt.Sprint(pre.Details) != fmt.Sprint(add.Details) {
				t.Fatalf("pre-check %q %v != AddShard %q %v", pre.Message, pre.Details, add.Message, add.Details)
			}
		})
	}
}

// TestCohortBuilder_AppendPrecheckLabelsStillLegal: rows that introduce
// labels the archive has never seen still append — the pre-check sees
// only pre-seeded dictionaries, so a union decided by the rows is
// AddShard's call at Close.
func TestCohortBuilder_AppendPrecheckLabelsStillLegal(t *testing.T) {
	p, _ := memBuilderEngine(t)
	buildCohort(t, p, "arch.pulse", precheckSchema(precheckBase()...), CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: 3}}, precheckRows())
	drop := []CohortRow{{uint64(9), "zeta", []string{"x", "q"}, nil}}
	res := buildCohort(t, p, "arch.pulse#drop.pulse", precheckSchema(precheckBase()...), CohortBuilderOptions{}, drop)
	if res.Records != 1 {
		t.Fatalf("append result = %+v", res)
	}
	assertRows(t, p, "arch.pulse#drop.pulse", drop)
}
