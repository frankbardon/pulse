package service

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"sync"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// openRecordReader opens path through the service and returns its
// record reader, closed at test cleanup.
func openRecordReader(t *testing.T, svc *Service, path string) *CohortRecordReader {
	t.Helper()
	c, err := svc.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open(%s): %v", path, err)
	}
	r, err := c.OpenRecordReader()
	if err != nil {
		t.Fatalf("OpenRecordReader(%s): %v", path, err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func readAllRows(t *testing.T, r *CohortRecordReader) [][]any {
	t.Helper()
	rows := make([][]any, r.Len())
	for i := range rows {
		row, err := r.RecordAt(int64(i))
		if err != nil {
			t.Fatalf("RecordAt(%d): %v", i, err)
		}
		rows[i] = row
	}
	return rows
}

// extractToFile writes one stored shard out as a standalone single-file
// cohort, the "read the shard individually" arm.
func extractToFile(t *testing.T, svc *Service, fsys afero.Fs, archive, shard, out string) {
	t.Helper()
	rc, err := svc.ExtractShard(context.Background(), archive, shard)
	if err != nil {
		t.Fatalf("ExtractShard(%s): %v", shard, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fsys, out, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// archiveCases builds the archives the reader must address: a grouped
// archive whose seed shard keeps a PREFIX of the union group and member
// dictionaries, a set archive widened set_u8 -> set_u16 by an AddShard,
// and a grouped archive whose group member set was widened.
func archiveCases(t *testing.T) map[string]func(t *testing.T) (*Service, afero.Fs, []string) {
	ctx := context.Background()
	return map[string]func(t *testing.T) (*Service, afero.Fs, []string){
		"grouped": func(t *testing.T) (*Service, afero.Fs, []string) {
			_, a2, _ := gShardA().build(t, nil)
			_, b2, _ := gShardB().build(t, nil)
			svc, fsys := gEnv(t, map[string][]byte{"a.pulse": a2, "b.pulse": b2})
			if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse", "b.pulse"}); err != nil {
				t.Fatal(err)
			}
			return svc, fsys, []string{"a.pulse", "b.pulse"}
		},
		"set-widened": func(t *testing.T) (*Service, afero.Fs, []string) {
			svc, fsys := setWidenFixture(t)
			if _, err := svc.AddShard(ctx, "arch.pulse", "add.pulse"); err != nil {
				t.Fatal(err)
			}
			return svc, fsys, []string{"seed.pulse", "add.pulse"}
		},
		"grouped-set-widened": func(t *testing.T) (*Service, afero.Fs, []string) {
			tagsA := []string{"t0", "t1", "t2", "t3", "t4", "t5"}
			tagsB := []string{"t6", "t7", "t8", "t9", "t10", "t11"}
			a := gShardA()
			a.tagRung, a.tagDict = encoding.FieldTypeSetU8, tagsA
			a.tagsOf = func(p int) []string { return []string{tagsA[p%6], tagsA[(p/3)%6]} }
			b := gShard{lo: 45, hi: 60, fanout: 5, idBase: 20000, regionDict: gRegions, src: "batch-a",
				tagRung: encoding.FieldTypeSetU8, tagDict: tagsB}
			b.tagsOf = func(p int) []string { return []string{tagsB[p%6], tagsB[(p/2)%6]} }
			_, a2, _ := a.build(t, nil)
			_, b2, _ := b.build(t, nil)
			svc, fsys := gEnv(t, map[string][]byte{"a.pulse": a2, "b.pulse": b2})
			if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse"}); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.AddShard(ctx, "arch.pulse", "b.pulse"); err != nil {
				t.Fatal(err)
			}
			return svc, fsys, []string{"a.pulse", "b.pulse"}
		},
	}
}

// TestCohortRecordReader_ArchiveEqualsShardsInOrder: RecordAt over a
// whole archive equals reading each shard individually (extracted to a
// standalone cohort), concatenated in archive order; Len is the sum.
func TestCohortRecordReader_ArchiveEqualsShardsInOrder(t *testing.T) {
	for name, build := range archiveCases(t) {
		t.Run(name, func(t *testing.T) {
			svc, fsys, shards := build(t)
			whole := openRecordReader(t, svc, "arch.pulse")
			var want [][]any
			for k, sh := range shards {
				out := fmt.Sprintf("x%d.pulse", k)
				extractToFile(t, svc, fsys, "arch.pulse", sh, out)
				want = append(want, readAllRows(t, openRecordReader(t, svc, out))...)
			}
			if len(want) == 0 {
				t.Fatal("fixture has no records")
			}
			if whole.Len() != int64(len(want)) {
				t.Fatalf("archive Len = %d, want %d (sum over shards)", whole.Len(), len(want))
			}
			got := readAllRows(t, whole)
			for i := range want {
				if !reflect.DeepEqual(got[i], want[i]) {
					t.Fatalf("record %d: archive %#v, shard %#v", i, got[i], want[i])
				}
			}
			for _, i := range []int64{-1, whole.Len()} {
				if _, err := whole.RecordAt(i); !errors.HasCode(err, errors.SERVICE_VALIDATION) {
					t.Fatalf("RecordAt(%d) = %v, want SERVICE_VALIDATION", i, err)
				}
			}
		})
	}
}

// TestCohortRecordReader_AnchorDecodesWithCanonicalSchema: an anchored
// shard reads with the archive's canonical schema and yields exactly the
// whole-archive reader's rows for that shard. A stored shard's own
// header schema is NOT byte-equal to canonical — the seed shard keeps a
// prefix of the union dictionaries — but it is decode-compatible (same
// structure, set rungs and group layout, every dictionary a prefix).
func TestCohortRecordReader_AnchorDecodesWithCanonicalSchema(t *testing.T) {
	prefixSeen := map[string]bool{}
	for name, build := range archiveCases(t) {
		t.Run(name, func(t *testing.T) {
			svc, _, shards := build(t)
			whole := openRecordReader(t, svc, "arch.pulse")
			canonical := whole.Schema()
			all := readAllRows(t, whole)
			off := 0
			sawPrefix := false
			for _, sh := range shards {
				anchor := "arch.pulse#" + sh
				c, err := svc.Open(context.Background(), anchor)
				if err != nil {
					t.Fatal(err)
				}
				own := c.Schema()
				if err := shardDecodeCompatible(canonical, own); err != nil {
					t.Fatalf("%s header schema not decode-compatible with canonical: %v", sh, err)
				}
				if !reflect.DeepEqual(dictsOf(own), dictsOf(canonical)) {
					sawPrefix = true
				}
				r := openRecordReader(t, svc, anchor)
				if !reflect.DeepEqual(dictsOf(r.Schema()), dictsOf(canonical)) ||
					!reflect.DeepEqual(r.Schema().Groups, canonical.Groups) {
					t.Fatalf("%s: anchor reader schema is not the canonical schema", sh)
				}
				rows := readAllRows(t, r)
				for i, row := range rows {
					if !reflect.DeepEqual(row, all[off+i]) {
						t.Fatalf("%s record %d: anchor %#v, archive %#v", sh, i, row, all[off+i])
					}
				}
				off += len(rows)
			}
			if off != len(all) {
				t.Fatalf("anchors cover %d records, archive %d", off, len(all))
			}
			prefixSeen[name] = sawPrefix
		})
	}
	// The grouped seed shard is stored untouched, so its member and group
	// dictionaries are a strict prefix of the union; an archive-wide set
	// widen rewrites every shard to the union instead.
	if !prefixSeen["grouped"] {
		t.Fatal("grouped fixture never exercises a shard whose dictionaries are a strict prefix of canonical")
	}
}

// dictsOf flattens a schema's member dictionaries and group entries.
func dictsOf(s *encoding.Schema) []any {
	var out []any
	for _, f := range s.Fields {
		if f.Dictionary != nil {
			out = append(out, f.Name, f.Type.String(), f.Dictionary.Values())
		}
	}
	for _, g := range s.Groups {
		out = append(out, g.Entries)
	}
	return out
}

// TestCohortRecordReader_RefusesIncompatibleShard: a shard a Pulse
// writer would never store — a flat shard in a grouped archive, or a
// group dictionary that is not a prefix of canonical — is refused at
// open with the code `shard verify` reports, never decoded wrongly.
func TestCohortRecordReader_RefusesIncompatibleShard(t *testing.T) {
	a1, a2, _ := gShardA().build(t, nil)
	b := gShardB()
	b.regionDict = gRegions
	_, b2, _ := b.build(t, nil)
	canonical, err := readSinglePulseSchema(a2)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		shard []byte
		code  errors.Code
	}{
		{"flat shard in a grouped archive", a1, errors.PULSE_SHARD_SCHEMA_MISMATCH},
		{"non-prefix group dictionary", b2, errors.PULSE_SHARD_DICT_DIVERGENCE},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := synthesizeArchiveWithRawPayload(t, canonical, []rawShard{{Name: "a.pulse", Payload: a2}, {Name: "x.pulse", Payload: tc.shard}})
			svc, _ := gEnv(t, map[string][]byte{"arch.pulse": data})
			for _, path := range []string{"arch.pulse", "arch.pulse#x.pulse"} {
				c, err := svc.Open(context.Background(), path)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := c.OpenRecordReader(); !errors.HasCode(err, tc.code) {
					t.Fatalf("%s: OpenRecordReader = %v, want %s", path, err, tc.code)
				}
			}
		})
	}
}

// TestCohortRecordReader_ArchiveConcurrent fans RecordAt across shard
// boundaries from many goroutines; run under -race.
func TestCohortRecordReader_ArchiveConcurrent(t *testing.T) {
	svc, _, _ := archiveCases(t)["grouped"](t)
	r := openRecordReader(t, svc, "arch.pulse")
	want := readAllRows(t, r)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				i := (g*131 + k*17) % len(want)
				row, err := r.RecordAt(int64(i))
				if err != nil {
					errs <- err
					return
				}
				if !reflect.DeepEqual(row, want[i]) {
					errs <- fmt.Errorf("record %d diverged under concurrency", i)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RecordAt(0); !errors.HasCode(err, errors.SERVICE_RESOURCE) {
		t.Fatalf("RecordAt after Close = %v, want SERVICE_RESOURCE", err)
	}
}
