package io

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

func tinyShardSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{{Name: "v", Type: encoding.FieldTypeU8}}}
}

// TestCohortBuild_ShardWarningsSurface: the archive publisher's
// warnings reach the report unchanged, AFTER the build's own findings
// (here a low-quality description), and the publisher sees one staged
// file per shard, named part-NNNNN.pulse, in order.
func TestCohortBuild_ShardWarningsSurface(t *testing.T) {
	fsys := afero.NewMemMapFs()
	shardWarn := errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_SET_WIDENED, "widened",
		map[string]any{"field": "tags"})
	var got []string
	b, err := NewCohortBuild(context.Background(), fsys, "arch.pulse", tinyShardSchema(), CohortBuildOptions{
		ShardMaxRecords: 2,
		Archive: func(_ context.Context, target string, paths []string) ([]*errors.CodedError, error) {
			if target != "arch.pulse" {
				t.Errorf("archive target %q", target)
			}
			for _, p := range paths {
				if ok, _ := afero.Exists(fsys, p); !ok {
					t.Errorf("staged shard %s missing at publish", p)
				}
			}
			got = paths
			return []*errors.CodedError{shardWarn}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		if err := b.Append([]any{uint64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := b.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("publisher saw %d shards, want 3", len(got))
	}
	want := []string{"part-00001.pulse", "part-00002.pulse", "part-00003.pulse"}
	for i, n := range want {
		if rep.Shards[i] != n {
			t.Fatalf("shards = %v, want %v", rep.Shards, want)
		}
	}
	if n := len(rep.Warnings); n < 2 || rep.Warnings[n-1] != shardWarn ||
		!errors.HasCode(rep.Warnings[0], errors.PULSE_FIELD_DESCRIPTION_LOW_QUALITY) {
		t.Fatalf("warnings = %v, want build findings then the shard warning", rep.Warnings)
	}
}

// TestCohortBuild_ShardPublishFailureLeavesNothing: a publisher error
// is Close's error, and the spool and the staging directory are gone.
func TestCohortBuild_ShardPublishFailureLeavesNothing(t *testing.T) {
	fsys := afero.NewMemMapFs()
	boom := stderrors.New("publish failed")
	b, err := NewCohortBuild(context.Background(), fsys, "arch.pulse", tinyShardSchema(), CohortBuildOptions{
		ShardMaxRecords: 1,
		Archive: func(context.Context, string, []string) ([]*errors.CodedError, error) {
			return nil, boom
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if err := b.Append([]any{uint64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); !stderrors.Is(err, boom) {
		t.Fatalf("Close = %v, want the publisher's error", err)
	}
	ents, err := afero.ReadDir(fsys, "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 0 {
		t.Fatalf("failed sharded Close left %d entries (first %s)", len(ents), ents[0].Name())
	}
}

// TestCohortBuild_ShardOptions: a negative shard size and a sharded
// build without a publisher are refused up front.
func TestCohortBuild_ShardOptions(t *testing.T) {
	pub := func(context.Context, string, []string) ([]*errors.CodedError, error) { return nil, nil }
	for _, opts := range []CohortBuildOptions{
		{ShardMaxRecords: -1, Archive: pub},
		{ShardMaxRecords: 3},
	} {
		fsys := afero.NewMemMapFs()
		if _, err := NewCohortBuild(context.Background(), fsys, "a.pulse", tinyShardSchema(), opts); !errors.HasCode(err, errors.SERVICE_VALIDATION) {
			t.Fatalf("opts %+v: err = %v, want SERVICE_VALIDATION", opts, err)
		}
	}
}

// TestCohortBuild_ShardLimit: the row that would open shard 65,536 is
// refused (the archive's shard count is a u16), leaves no trace, and
// the build stays usable.
func TestCohortBuild_ShardLimit(t *testing.T) {
	fsys := afero.NewMemMapFs()
	b, err := NewCohortBuild(context.Background(), fsys, "a.pulse", tinyShardSchema(), CohortBuildOptions{
		ShardMaxRecords: 1,
		Archive:         func(context.Context, string, []string) ([]*errors.CodedError, error) { return nil, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Abort() }()
	for i := range MaxBuildShards {
		if err := b.Append([]any{uint64(i % 200)}); err != nil {
			t.Fatalf("Append(%d): %v", i, err)
		}
	}
	err = b.Append([]any{uint64(1)})
	if !errors.HasCode(err, errors.SERVICE_VALIDATION) {
		t.Fatalf("Append past the shard limit = %v, want SERVICE_VALIDATION", err)
	}
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Details["reason"] != "shard_limit" {
		t.Fatalf("details = %v, want reason shard_limit", err)
	}
	if b.records != MaxBuildShards {
		t.Fatalf("records = %d after the refusal, want %d", b.records, MaxBuildShards)
	}
}
