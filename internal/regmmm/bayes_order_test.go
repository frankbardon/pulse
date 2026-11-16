package regmmm

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// bayes_order_test.go: REG_MMM_BAYES reads the shared
// RegressionSpec.Order as its time field, params.time staying its
// deprecated alias. The order spelling fits — output and derived seed —
// exactly what the alias fits (the digest the alias pinned in
// bayes_digest_test.go), and the alias / panel conflicts are the
// factory's spec refusals (predict raises them through
// BayesSpecRefusal).

// orderDigest is the SHA-256 of the fit of spec over digestRows.
func orderDigest(t *testing.T, spec *types.RegressionSpec) string {
	t.Helper()
	reg, err := BayesFactory(spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := reg.(extend.ContextRegression).FitContext(context.Background(), digestRows())
	if err != nil {
		t.Fatal(err)
	}
	body, err := types.MarshalFinite(out)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(body))
}

// TestMMMBayes_OrderIsTheTimeAlias: order {field: week} without
// params.time, and with the same params.time beside it, give the alias
// fit's pinned digest under the derived seed and under an explicit one.
func TestMMMBayes_OrderIsTheTimeAlias(t *testing.T) {
	t.Parallel()

	const tail = `"chains":1,"warmup":40,"draws":30}`
	for _, tc := range []struct {
		name   string
		params string
		seed   int64
		want   string
	}{
		{"order_only_derived_seed", `{"channels":["tv"],` + tail, 0, "c5253d29b46fd7911b0cb3de70c061ab0f69138ea7449736e77c2f74b21fcab3"},
		{"order_and_alias_derived_seed", `{"channels":["tv"],"time":"week",` + tail, 0, "c5253d29b46fd7911b0cb3de70c061ab0f69138ea7449736e77c2f74b21fcab3"},
		{"order_only_explicit_seed", `{"channels":["tv"],` + tail, 99, "e1b68270343c09c00e6ceafdfd4c76aa2af90875bb6a6c648a4a7e05607662b0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			spec := BayesSpec(tc.params)
			spec.RNGSeed = tc.seed
			spec.Order = &types.RegressionOrder{Field: "week"}
			if got := orderDigest(t, spec); got != tc.want {
				t.Errorf("digest %s, want %s (the params.time fit)", got, tc.want)
			}
		})
	}
}

// TestMMMBayes_OrderSpecRefusals: the alias conflict and the panel rule
// are PROCESSING_CONFIG {slot: order, reason}, raised by the factory and
// by BayesSpecRefusal (predict's call) alike; an absent time in both
// spellings keeps the params.time refusal.
func TestMMMBayes_OrderSpecRefusals(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		params string
		order  *types.RegressionOrder
		slot   string
		reason string
	}{
		{"alias_conflict", `{"channels":["tv"],"time":"day"}`, &types.RegressionOrder{Field: "week"}, "order", reasonTimeAliasConflict},
		{"partition_without_geo", `{"channels":["tv"]}`, &types.RegressionOrder{Field: "week", Partition: "market"}, "order", reasonOrderPartitionGeo},
		{"geo_without_partition", `{"channels":["tv"],"geo":"market"}`, &types.RegressionOrder{Field: "week"}, "order", reasonOrderPartitionGeo},
		{"geo_other_partition", `{"channels":["tv"],"geo":"market"}`, &types.RegressionOrder{Field: "week", Partition: "region"}, "order", reasonOrderPartitionGeo},
		{"no_time_at_all", `{"channels":["tv"]}`, nil, "params.time", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			spec := BayesSpec(tc.params)
			spec.Order = tc.order
			_, err := BayesFactory(spec, nil)
			ce := BayesSpecRefusal(spec)
			if err == nil || ce == nil {
				t.Fatalf("factory %v, BayesSpecRefusal %v: want both to refuse", err, ce)
			}
			if err.Error() != ce.Error() {
				t.Errorf("factory %q != predict %q", err, ce)
			}
			if ce.Code != perr.PROCESSING_CONFIG || ce.Details["slot"] != tc.slot {
				t.Errorf("got %s %v, want PROCESSING_CONFIG slot %s", ce.Code, ce.Details, tc.slot)
			}
			if tc.reason != "" && ce.Details["reason"] != tc.reason {
				t.Errorf("reason %v, want %s", ce.Details["reason"], tc.reason)
			}
		})
	}
	// The panel's own spelling is accepted: partition = params.geo.
	spec := BayesSpec(`{"channels":["tv"],"geo":"market"}`)
	spec.Order = &types.RegressionOrder{Field: "week", Partition: "market"}
	if ce := BayesSpecRefusal(spec); ce != nil {
		t.Errorf("partition = geo refused: %v", ce)
	}
}
