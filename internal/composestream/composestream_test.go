package composestream

import (
	"bytes"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestWrite_RequestOnlyRecords: with no response, Write emits only the
// records the request alone decides — a ranked sweep's empty ranking
// (+ an empty profile under rank.profile) and, under TerminalFits, an
// empty fits object last — and nothing for a nil request.
func TestWrite_RequestOnlyRecords(t *testing.T) {
	t.Parallel()

	req := &types.ComposedRequest{
		Sweep: &types.SweepSpec{Rank: &types.SweepRank{By: "n", Profile: true}},
		Fits:  []types.FitSpec{{Name: "m"}},
	}
	for _, tc := range []struct {
		name string
		req  *types.ComposedRequest
		opts Options
		want string
	}{
		{"off", req, Options{}, "{\"ranking\":[],\"ranking_profile\":{}}\n"},
		{"terminal fits", req, Options{TerminalFits: true}, "{\"ranking\":[],\"ranking_profile\":{}}\n{\"fits\":{}}\n"},
		{"nil request", nil, Options{TerminalFits: true}, ""},
	} {
		var buf bytes.Buffer
		if err := Write(&buf, tc.req, nil, tc.opts); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := buf.String(); got != tc.want {
			t.Errorf("%s: wrote %q, want %q", tc.name, got, tc.want)
		}
	}
}
