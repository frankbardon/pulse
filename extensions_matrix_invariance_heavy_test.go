//go:build heavy

package pulse

import (
	"context"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/observe"
	"github.com/spf13/afero"
)

// TestExtensionMatrix_WorkerInvariant is the gate. It also holds
// predict to the runtime: the co-moment tier is predicted Mergeable and
// takes every parallel arm; the buffered tier is predicted neither
// streamable nor mergeable and runs serially under every knob.
func TestExtensionMatrix_WorkerInvariant(t *testing.T) {
	dir := t.TempDir()
	// Parallel decode needs mmap: the cohorts live on a real (temporary)
	// directory.
	fsys := afero.NewBasePathFs(afero.NewOsFs(), dir)
	serial := newExtInvEngine(t, fsys, true, 1, 1)
	extInvWrite(t, serial.p, "big.pulse", 0, extInvRows)
	extInvWrite(t, serial.p, "twin.pulse", 0, extInvTwinRows)
	var shards []string
	for i := 0; i+1 < len(extInvShardBounds); i++ {
		path := fmt.Sprintf("inv_s%d.pulse", i)
		extInvWrite(t, serial.p, path, extInvShardBounds[i], extInvShardBounds[i+1])
		shards = append(shards, path)
	}
	if _, err := serial.p.CreateShardArchive(context.Background(), "archive.pulse", shards); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}

	decoders := map[int]*extInvEngine{}
	for _, dw := range []int{2, 3, 7, 8} {
		decoders[dw] = newExtInvEngine(t, fsys, true, 1, dw)
	}
	sharders := map[[2]int]*extInvEngine{}
	for _, k := range [][2]int{{1, 1}, {2, 1}, {3, 1}, {8, 1}, {3, 3}, {8, 7}} {
		sharders[k] = newExtInvEngine(t, fsys, true, k[0], k[1])
	}
	bufSerial := newExtInvEngine(t, fsys, false, 1, 1)
	bufWide := newExtInvEngine(t, fsys, false, 8, 8)

	for _, tier := range []struct {
		name string
		e    *extInvEngine
		want bool
	}{{"co-moment", serial, true}, {"buffered", bufSerial, false}} {
		res, err := tier.e.p.Predict(context.Background(), extInvRequest("big.pulse", extInvVariant{missing: "pairwise", weighted: true, grouped: true}))
		if err != nil || !res.Valid || len(res.Matrices) != 1 {
			t.Fatalf("%s predict: err=%v res=%+v", tier.name, err, res)
		}
		if res.Matrices[0].Mergeable != tier.want || res.Matrices[0].Streamable != tier.want || res.Streamable != tier.want {
			t.Fatalf("%s predict: matrix streamable=%v mergeable=%v request streamable=%v, want %v",
				tier.name, res.Matrices[0].Streamable, res.Matrices[0].Mergeable, res.Streamable, tier.want)
		}
	}

	for _, missing := range []string{"listwise", "pairwise"} {
		for _, weighted := range []bool{false, true} {
			for _, grouped := range []bool{false, true} {
				v := extInvVariant{missing: missing, weighted: weighted, grouped: grouped}
				t.Run(v.String(), func(t *testing.T) {
					want, arm := serial.run(t, extInvRequest("big.pulse", v))
					if arm == observe.ArmParallelDecode || arm == observe.ArmShardParallel {
						t.Fatalf("serial run took arm %q", arm)
					}
					// The fixture is not degenerate: three buckets when
					// grouped, nulls thin the listwise N, weights move W.
					if n := len(want.Matrices); (grouped && n != 3) || (!grouped && n != 1) {
						t.Fatalf("%d matrix results", n)
					}
					s := want.Matrices[0].Scalars
					if missing == "listwise" && !grouped && s["n"] >= float64(extInvRows) {
						t.Fatalf("listwise n = %v — the nulls did not thin it", s["n"])
					}
					if weighted == (s["w"] == s["n"]) {
						t.Fatalf("weighted=%v but w = %v, n = %v", weighted, s["w"], s["n"])
					}

					for _, dw := range []int{2, 3, 7, 8} {
						got, arm := decoders[dw].run(t, extInvRequest("big.pulse", v))
						if arm != observe.ArmParallelDecode {
							t.Fatalf("DecodeWorkers=%d ran on arm %q — the decode reducer did not engage", dw, arm)
						}
						if d := extInvDiff(got, want); d != "" {
							t.Errorf("DecodeWorkers=%d differs from serial: %s", dw, d)
						}
					}

					twin, _ := serial.run(t, extInvRequest("twin.pulse", v))
					for k, e := range sharders {
						got, arm := e.run(t, extInvRequest("archive.pulse", v))
						if k[0] > 1 && arm != observe.ArmShardParallel {
							t.Fatalf("ShardWorkers=%d ran on arm %q — the shard reducer did not engage", k[0], arm)
						}
						if d := extInvDiff(got, twin); d != "" {
							t.Errorf("archive ShardWorkers=%d DecodeWorkers=%d differs from its single-file twin: %s", k[0], k[1], d)
						}
					}

					// The buffered tier: serial under every knob, the
					// same rows in the same order, the co-moment tier's
					// bits.
					buf, _ := bufSerial.run(t, extInvRequest("big.pulse", v))
					if _, ok := buf.Matrices[0].Scalars["row_digest"]; !ok {
						t.Fatal("buffered tier: Finalize saw no rows")
					}
					if d := extInvDiff(buf, want, "row_digest"); d != "" {
						t.Errorf("buffered tier differs from the co-moment tier: %s", d)
					}
					for _, c := range []struct{ cohort, label string }{{"big.pulse", "file"}, {"archive.pulse", "archive"}} {
						ref := buf
						if c.cohort == "archive.pulse" {
							ref, _ = bufSerial.run(t, extInvRequest("archive.pulse", v))
						}
						got, arm := bufWide.run(t, extInvRequest(c.cohort, v))
						if arm == observe.ArmParallelDecode || arm == observe.ArmShardParallel {
							t.Fatalf("buffered tier %s ran on parallel arm %q", c.label, arm)
						}
						if d := extInvDiff(got, ref); d != "" {
							t.Errorf("buffered tier %s ShardWorkers=8 DecodeWorkers=8 differs from serial: %s", c.label, d)
						}
					}
				})
			}
		}
	}
}
