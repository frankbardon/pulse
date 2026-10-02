package gosdk_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/mcp/gosdk"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/afero"
)

// readSchemaResource mounts p on a fresh server and returns the text of
// the pulse://schema resource.
func readSchemaResource(t *testing.T, p *pulse.Pulse) string {
	t.Helper()
	srv := newServer()
	if err := gosdk.Register(srv, p, gosdk.Config{Version: "9.9.9", DisableCohortScan: true}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	c, cancel := connect(t, srv)
	defer cancel()
	out, err := c.ReadResource(context.Background(), &mcpsdk.ReadResourceParams{URI: gosdk.SchemaResourceURI})
	if err != nil {
		t.Fatalf("ReadResource(%s): %v", gosdk.SchemaResourceURI, err)
	}
	if len(out.Contents) != 1 {
		t.Fatalf("contents = %d, want 1", len(out.Contents))
	}
	return out.Contents[0].Text
}

// TestSchemaResource_ServesInstanceSchema pins that pulse://schema
// serves the instance's payload schema (Pulse.PayloadSchema): a
// profile-free server is byte-identical to the published full-registry
// schema, while a feature-profiled server carries its own digest in the
// root $comment and never names a hidden operator.
func TestSchemaResource_ServesInstanceSchema(t *testing.T) {
	t.Run("profile-free unchanged", func(t *testing.T) {
		p := newPulse(t, afero.NewMemMapFs())
		got := readSchemaResource(t, p)
		if want := string(descx.BuildPayloadSchema()); got != want {
			t.Fatalf("profile-free pulse://schema differs from BuildPayloadSchema (%d vs %d bytes)", len(got), len(want))
		}
	})

	t.Run("feature profile scopes it", func(t *testing.T) {
		p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), FeatureProfile: &pulse.FeatureProfile{
			Features: []string{"capability:process", "AGG_COUNT", "AGG_SUM", "GROUP_CATEGORY", "GROUP_RANGE"},
		}})
		if err != nil {
			t.Fatalf("pulse.New: %v", err)
		}
		got := readSchemaResource(t, p)

		want, err := p.PayloadSchema()
		if err != nil {
			t.Fatalf("PayloadSchema: %v", err)
		}
		if got != string(want) {
			t.Fatalf("profiled pulse://schema differs from p.PayloadSchema()")
		}
		var root map[string]any
		if err := json.Unmarshal([]byte(got), &root); err != nil {
			t.Fatalf("schema is not JSON: %v", err)
		}
		if c, _ := root["$comment"].(string); c != "feature_set_digest: "+p.FeatureSetDigest() {
			t.Errorf("$comment = %q, want the instance digest %q", c, p.FeatureSetDigest())
		}
		for _, hidden := range []string{"AGG_MEAN", "GROUP_DATE", "ComposedRequest"} {
			if strings.Contains(got, hidden) {
				t.Errorf("profiled schema names hidden %s", hidden)
			}
		}
		if !strings.Contains(got, "AGG_SUM") {
			t.Errorf("profiled schema lost enabled AGG_SUM")
		}
	})
}
