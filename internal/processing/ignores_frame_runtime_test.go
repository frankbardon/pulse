package processing

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/processing/window"
	"github.com/frankbardon/pulse/types"
)

// TestWindows_IgnoresFrameMatchesRuntime is the runtime half of the
// Operator.IgnoresFrame claim, which decides whether a materialized
// window column's description states a frame.
//
// Every built-in window predict requires a frame for is run over one
// ordered series under three different bounded frames. An operator
// that declares IgnoresFrame must give the SAME column under all three
// (otherwise the description omits a frame that shaped the values); one
// that does not declare it must give DIFFERENT columns (otherwise the
// flag is missing and the description echoes a frame nothing read).
func TestWindows_IgnoresFrameMatchesRuntime(t *testing.T) {
	t.Parallel()

	m := descx.BuildManifest()
	one, two, zero := 1, 2, 0
	frames := []*types.FrameSpec{
		{Mode: "rows", Preceding: &zero, Following: &zero},
		{Mode: "rows", Preceding: &one, Following: &zero},
		{Mode: "rows", Preceding: &two, Following: &one},
	}
	params := map[string]json.RawMessage{string(types.WIN_EWMA): json.RawMessage(`{"alpha":0.5}`)}
	framed := 0
	for _, op := range m.Components.Windows {
		if !descx.WindowFrameRequired(types.WindowType(op.Name)) {
			if op.IgnoresFrame {
				t.Errorf("%s declares IgnoresFrame but takes no frame", op.Name)
			}
			continue
		}
		framed++
		t.Run(op.Name, func(t *testing.T) {
			var cols []string
			for _, f := range frames {
				rows := make([]map[string]any, 6)
				for i := range rows {
					rows[i] = map[string]any{"t": float64(i), "x": float64((i*7)%5 + 1)}
				}
				w := &types.Window{Type: types.WindowType(op.Name), Field: "x", Label: "out",
					OrderBy: []types.OrderKey{{Field: "t"}}, Frame: f, Params: params[op.Name]}
				if err := window.Apply(context.Background(), rows, []*types.Window{w}); err != nil {
					t.Fatal(err)
				}
				col := ""
				for _, r := range rows {
					col += fmt.Sprintf("%v;", r["out"])
				}
				cols = append(cols, col)
			}
			same := cols[0] == cols[1] && cols[1] == cols[2]
			switch {
			case op.IgnoresFrame && !same:
				t.Errorf("declares IgnoresFrame, but the frame changed the column: %q", cols)
			case !op.IgnoresFrame && same:
				t.Errorf("reads no frame (identical column %q under every frame) — declare IgnoresFrame", cols[0])
			}
		})
	}
	if framed == 0 {
		t.Fatal("no frame-taking window in the manifest")
	}
}
