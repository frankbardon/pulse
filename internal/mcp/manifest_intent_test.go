package mcp

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
)

func invokeManifest(t *testing.T, args string) ([]byte, error) {
	t.Helper()
	p := defaultPulse(t)
	for _, td := range Tools(Config{}) {
		if td.Name != toolmeta.ToolManifest {
			continue
		}
		out, err := td.Invoke(context.Background(), p, json.RawMessage(args))
		if err != nil {
			return nil, err
		}
		body, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		return body, nil
	}
	t.Fatal("pulse_manifest not in the catalog")
	return nil, nil
}

// TestManifest_IntentArg: no intent is the unchanged slim manifest; an
// intent serves the slim form of p.ManifestForIntent with the elided
// keys absent and scope set; an unknown one is the coded recommend
// error, not a stringified one.
func TestManifest_IntentArg(t *testing.T) {
	p := defaultPulse(t)
	ctx := context.Background()

	plain, err := invokeManifest(t, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(descx.SlimManifest(p.Manifest(ctx)))
	if string(plain) != string(want) {
		t.Fatal("no-intent pulse_manifest differs from the slim p.Manifest")
	}

	scoped, err := invokeManifest(t, `{"intent":"compare_groups"}`)
	if err != nil {
		t.Fatal(err)
	}
	facade, err := p.ManifestForIntent(ctx, "compare_groups")
	if err != nil {
		t.Fatal(err)
	}
	if fw, _ := json.Marshal(descx.SlimManifest(facade)); string(scoped) != string(fw) {
		t.Error("handler and facade disagree")
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(scoped, &wire); err != nil {
		t.Fatal(err)
	}
	for _, k := range descriptor.ScopedManifestElidedKeys() {
		if _, ok := wire[k]; ok {
			t.Errorf("elided %s present", k)
		}
	}
	var scope descriptor.ManifestScope
	if err := json.Unmarshal(wire["scope"], &scope); err != nil || scope.Intent.ID != "compare_groups" {
		t.Errorf("scope = %s (%v)", wire["scope"], err)
	}
	if len(scoped)*5 > len(plain) {
		t.Errorf("scoped %d bytes is not a small fraction of %d", len(scoped), len(plain))
	}

	_, err = invokeManifest(t, `{"intent":"nope"}`)
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_RECOMMEND_INTENT_UNKNOWN {
		t.Fatalf("unknown intent: err = %v", err)
	}
}
