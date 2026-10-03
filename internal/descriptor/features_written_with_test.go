package descriptor

import "testing"

func TestProfileWrittenWith(t *testing.T) {
	cases := map[string]string{
		"1.0.0":                     "1.0.0",
		"v1.2.3":                    "1.2.3",
		"1.0.0-alpha.2":             "1.0.0",
		"v1.1.0-alpha.3-4-gdeadbee": "1.1.0",
		"2.0.1+meta":                "2.0.1",
		// No usable core: the newest Since in the table.
		"devel":              BuiltinFeatureSince,
		"devel+0123456789ab": BuiltinFeatureSince,
		"0.0.0-2026-abcdef":  BuiltinFeatureSince,
		"":                   BuiltinFeatureSince,
	}
	for running, want := range cases {
		if got := ProfileWrittenWith(running); got != want {
			t.Errorf("ProfileWrittenWith(%q) = %q; want %q", running, got, want)
		}
	}
}
