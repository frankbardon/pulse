//go:build heavy

package regmmm

import "testing"

// TestMMMBayes_LiftAbsentDigest: params.lift_tests empty (or null) is the
// lift-free fit — the geometric Hill digest of
// TestMMMBayes_GeometricDefaultDigest under the same explicit seed —
// so the lift likelihood adds nothing, not even a rounding, when no test
// is given. (Absent lift_tests is that test's own rows.)
func TestMMMBayes_LiftAbsentDigest(t *testing.T) {
	t.Parallel()

	const base = `{"channels":["tv"],"time":"week","chains":1,"warmup":40,"draws":30`
	const want = "e1b68270343c09c00e6ceafdfd4c76aa2af90875bb6a6c648a4a7e05607662b0"
	for _, params := range []string{base + `,"lift_tests":[]}`, base + `,"lift_tests":null}`} {
		if got := fitDigest(t, params, 99); got != want {
			t.Errorf("%s: digest %s, want %s", params, got, want)
		}
	}
}
