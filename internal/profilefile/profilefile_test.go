package profilefile

import (
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

func TestReadOS(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	good := write("good.json", `{"profile":"p","features":["AGG_COUNT"]}`)
	fp, err := ReadOS(good)
	if err != nil {
		t.Fatalf("good profile: %v", err)
	}
	if fp.Profile != "p" || len(fp.Features) != 1 || fp.Features[0] != "AGG_COUNT" {
		t.Errorf("good profile decoded as %+v", fp)
	}

	cases := []struct {
		name, path, reason string
	}{
		{"unreadable", filepath.Join(dir, "absent.json"), "file_unreadable"},
		{"unknown key", write("key.json", `{"features":[],"bogus":1}`), "unknown_key"},
		{"malformed", write("bad.json", `{`), "malformed_json"},
		{"missing features", write("none.json", `{}`), "missing_features"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ReadOS(tc.path)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_FEATURE_PROFILE_INVALID {
				t.Fatalf("err = %v, want PULSE_FEATURE_PROFILE_INVALID", err)
			}
			if ce.Details["reason"] != tc.reason {
				t.Errorf("reason = %v, want %s", ce.Details["reason"], tc.reason)
			}
			if ce.Details["path"] != tc.path {
				t.Errorf("path detail = %v, want %s", ce.Details["path"], tc.path)
			}
			if !strings.Contains(ce.Message, tc.path) {
				t.Errorf("message %q does not name the path", ce.Message)
			}
		})
	}
}
