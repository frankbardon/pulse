package window

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

func TestResolveFrame(t *testing.T) {
	cases := []struct {
		name      string
		rowIdx    int
		partLen   int
		preceding *int
		following *int
		wantLo    int
		wantHi    int
	}{
		{"unbounded both", 3, 10, nil, nil, 0, 9},
		{"unbounded preceding, current row", 5, 10, nil, ptrInt(0), 0, 5},
		{"current row only", 4, 10, ptrInt(0), ptrInt(0), 4, 4},
		{"3 preceding, 0 following", 6, 10, ptrInt(3), ptrInt(0), 3, 6},
		{"clamp lo at start", 1, 10, ptrInt(5), ptrInt(0), 0, 1},
		{"clamp hi at end", 8, 10, ptrInt(0), ptrInt(5), 8, 9},
		{"2 preceding, 2 following middle", 4, 10, ptrInt(2), ptrInt(2), 2, 6},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frame := &types.FrameSpec{Mode: "rows", Preceding: tc.preceding, Following: tc.following}
			gotLo, gotHi := resolveFrame(tc.rowIdx, tc.partLen, frame)
			if gotLo != tc.wantLo || gotHi != tc.wantHi {
				t.Errorf("resolveFrame(%d, %d) = (%d, %d), want (%d, %d)",
					tc.rowIdx, tc.partLen, gotLo, gotHi, tc.wantLo, tc.wantHi)
			}
		})
	}
}

func ptrInt(v int) *int { return &v }
