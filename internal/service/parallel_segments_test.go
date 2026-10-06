package service

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/linalg"
)

// TestBlockAlignedSegments_Math: for every N and W the parallel-decode
// partition covers exactly [0, N) with contiguous segments, every
// boundary is a multiple of linalg.MergeBlockSize (so no block straddles
// two workers), the ragged tail lands in the last segment, whole blocks
// are spread so segment block counts differ by at most one, and
// W > blocks leaves the surplus segments empty rather than failing.
func TestBlockAlignedSegments_Math(t *testing.T) {
	B := linalg.MergeBlockSize
	ns := []int{1, B - 1, B, B + 1, 3*B - 7, 24 * B, parallelDecodeRecordThreshold,
		25 * B, 25*B + 1, 1_000_003}
	ws := []int{1, 2, 3, 7, 8, 16, 25, 26, 64, 200}
	for _, n := range ns {
		for _, w := range ws {
			segs := blockAlignedSegments(n, w)
			if len(segs) != w {
				t.Fatalf("N=%d W=%d: %d segments, want %d", n, w, len(segs), w)
			}
			nBlocks := (n + B - 1) / B
			next, nonEmpty := 0, 0
			minBlk, maxBlk := int(^uint(0)>>1), 0
			for i, s := range segs {
				if s.startRec != next {
					t.Fatalf("N=%d W=%d seg %d: start %d, want %d (gap/overlap)", n, w, i, s.startRec, next)
				}
				if s.endRec < s.startRec {
					t.Fatalf("N=%d W=%d seg %d: end %d < start %d", n, w, i, s.endRec, s.startRec)
				}
				if s.startRec%B != 0 {
					t.Fatalf("N=%d W=%d seg %d: start %d not a multiple of %d", n, w, i, s.startRec, B)
				}
				if i < w-1 && s.endRec%B != 0 {
					t.Fatalf("N=%d W=%d seg %d: interior end %d not a multiple of %d", n, w, i, s.endRec, B)
				}
				if s.endRec > s.startRec {
					nonEmpty++
				}
				blk := (s.endRec - s.startRec + B - 1) / B
				minBlk, maxBlk = min(minBlk, blk), max(maxBlk, blk)
				next = s.endRec
			}
			if next != n {
				t.Fatalf("N=%d W=%d: partition ends at %d, want %d", n, w, next, n)
			}
			if want := min(w, nBlocks); nonEmpty != want {
				t.Fatalf("N=%d W=%d: %d non-empty segments, want %d", n, w, nonEmpty, want)
			}
			if maxBlk-minBlk > 1 {
				t.Fatalf("N=%d W=%d: segment block counts range %d..%d (unbalanced)", n, w, minBlk, maxBlk)
			}
			if last := segs[w-1]; last.endRec-last.startRec == 0 {
				t.Fatalf("N=%d W=%d: last segment empty; the ragged tail must land there", n, w)
			}
		}
	}
	if segs := blockAlignedSegments(0, 4); len(segs) != 4 || segs[3].endRec != 0 {
		t.Fatalf("N=0: %+v", segs)
	}
}

// idCohort is a one-field u32 record region whose value IS the absolute
// record index, so a callback can check the record it was handed.
func idCohort(t *testing.T, n int) *parallelDecodeContext {
	t.Helper()
	schema := &encoding.Schema{Fields: []encoding.Field{{Name: "id", Type: encoding.FieldTypeU32}}}
	stride := schema.RecordByteSize()
	buf := bytes.NewBuffer(make([]byte, 0, n*stride))
	for r := 0; r < n; r++ {
		if err := encoding.WriteFieldValue(buf, encoding.FieldTypeU32, uint64(r)); err != nil {
			t.Fatal(err)
		}
	}
	return &parallelDecodeContext{
		schema:       schema,
		mmapBytes:    buf.Bytes(),
		stride:       stride,
		totalRecords: n,
		binding:      processing.BindRecords(schema, nil),
	}
}

// TestParallelDecodeMmap_FactoryReceivesAbsoluteStart: every worker's
// factory is told its segment's ABSOLUTE start record and its count,
// they match blockAlignedSegments, and the callback's i-th record is
// record start+i — so a block-keyed reducer can key row start+i to
// block (start+i)/MergeBlockSize. Empty segments never call the factory.
func TestParallelDecodeMmap_FactoryReceivesAbsoluteStart(t *testing.T) {
	B := linalg.MergeBlockSize
	for _, tc := range []struct{ n, w int }{
		{3*B + 17, 2}, {3*B + 17, 3}, {10 * B, 7}, {2*B + 1, 8}, {B - 3, 4},
	} {
		pctx := idCohort(t, tc.n)
		type seen struct{ start, count, got int }
		var mu sync.Mutex
		calls := map[int]seen{}
		err := parallelDecodeMmap(context.Background(), pctx, tc.w,
			func(workerIdx, startRecord, recordCount int) DecodeCallback {
				i := 0
				return func(rec *processing.Record) error {
					v, ok := rec.NumericValue("id")
					if !ok || int(v) != startRecord+i {
						t.Errorf("N=%d W=%d worker %d: record %d of segment holds id %v, want %d",
							tc.n, tc.w, workerIdx, i, v, startRecord+i)
					}
					i++
					mu.Lock()
					calls[workerIdx] = seen{startRecord, recordCount, i}
					mu.Unlock()
					return nil
				}
			})
		if err != nil {
			t.Fatalf("N=%d W=%d: %v", tc.n, tc.w, err)
		}
		segs := blockAlignedSegments(tc.n, tc.w)
		total := 0
		for w, s := range segs {
			c, ok := calls[w]
			if s.endRec == s.startRec {
				if ok {
					t.Fatalf("N=%d W=%d: empty segment %d reached its callback", tc.n, tc.w, w)
				}
				continue
			}
			if !ok || c.start != s.startRec || c.count != s.endRec-s.startRec || c.got != c.count {
				t.Fatalf("N=%d W=%d worker %d: factory saw %+v, segment %+v", tc.n, tc.w, w, c, s)
			}
			total += c.got
		}
		if total != tc.n {
			t.Fatalf("N=%d W=%d: %d records decoded", tc.n, tc.w, total)
		}
	}
}
