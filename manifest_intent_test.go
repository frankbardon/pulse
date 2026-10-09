package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// TestManifestForIntent_UnderEveryShippedProfile: under every shipped
// feature profile, each intent the instance lists scopes cleanly — the
// instance digest, no hidden operator anywhere in the payload — and each
// intent it prunes is PULSE_RECOMMEND_INTENT_UNKNOWN.
func TestManifestForIntent_UnderEveryShippedProfile(t *testing.T) {
	ctx := context.Background()
	for name, p := range shippedProfilePulses(t) {
		t.Run(name, func(t *testing.T) {
			listed := p.Manifest(ctx).Intents
			hidden := hiddenOperatorNames(p)
			for _, id := range descx.IntentIDs() {
				m, err := p.ManifestForIntent(ctx, id)
				if !slices.Contains(listed, id) {
					var ce *perr.CodedError
					if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_RECOMMEND_INTENT_UNKNOWN {
						t.Errorf("pruned intent %s: err = %v", id, err)
					}
					continue
				}
				if err != nil {
					t.Fatalf("%s: %v", id, err)
				}
				if m.FeatureSetDigest != p.FeatureSetDigest() || m.Scope == nil || m.Scope.Intent.ID != id {
					t.Errorf("%s: digest %s / scope %+v", id, m.FeatureSetDigest, m.Scope)
				}
				body, _ := json.Marshal(m)
				for h := range hidden {
					if strings.Contains(string(body), strconv.Quote(h)) {
						t.Errorf("%s: scoped manifest names hidden %s", id, h)
					}
				}
			}
		})
	}
}
