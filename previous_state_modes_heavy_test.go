//go:build heavy

package pulse_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestPreviousState_ParallelDecodeMatchesSerial: over an on-disk cohort
// past the parallel-decode threshold — the panel interleaved with
// filler subjects prevModeKeep drops — DecodeWorkers 3 answers every
// matrix request exactly as DecodeWorkers 1 does (Components included),
// and its data, crosstab and warnings equal the panel's single-file
// answer. The non-crosstab requests decline the parallel reducer (not
// mergeable) and run buffered; a crosstab decodes its records in
// parallel segments, but the attribute still scores the whole decoded
// set, never a segment.
func TestPreviousState_ParallelDecodeMatchesSerial(t *testing.T) {
	want, _, _ := prevModeBaseline(t)
	fs := afero.NewBasePathFs(afero.NewOsFs(), t.TempDir())
	serial, _ := prevModeEngine(t, fs, pulse.Options{DecodeWorkers: 1})
	rows := make([][3]any, 0, len(prevModeRows)+prevModeDecodeFiller)
	every := (len(prevModeRows) + prevModeDecodeFiller) / len(prevModeRows)
	next := 0
	for i := 0; i < len(prevModeRows)+prevModeDecodeFiller; i++ {
		if next < len(prevModeRows) && i == next*every {
			rows = append(rows, prevModeRows[next])
			next++
			continue
		}
		rows = append(rows, [3]any{uint64(1_000_000 + i), int32(1), "free"})
	}
	panelCohort(t, serial, "panel_big.pulse", rows)
	parallel, arm := prevModeEngine(t, fs, pulse.Options{DecodeWorkers: 3})
	for name, mk := range prevModeRequests("panel_big.pulse") {
		t.Run(name, func(t *testing.T) {
			ser := prevModeRun(t, serial, mk(), true)
			if got := prevModeRun(t, parallel, mk(), true); got != ser {
				t.Errorf("DecodeWorkers 3\n%s\nDecodeWorkers 1\n%s", got, ser)
			}
			// A crosstab must take the parallel segment decode (the arm
			// this test exists for); every other request declines the
			// parallel reducer.
			wantArm := observe.ArmBuffered
			if strings.Contains(name, "crosstab") {
				wantArm = observe.ArmParallelDecode
			}
			if a := arm.last(); a != wantArm {
				t.Errorf("ran on arm %q, want %q", a, wantArm)
			}
			var single types.Response
			if err := json.Unmarshal([]byte(want[name]), &single); err != nil {
				t.Fatal(err)
			}
			if got, w := prevModeRun(t, parallel, mk(), false), prevModeDoc(t, &single, false); got != w {
				t.Errorf("parallel decode\n%s\nsingle-file panel\n%s", got, w)
			}
		})
	}
}
