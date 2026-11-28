package regmmm

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// bayes_order_test.go: REG_MMM_BAYES reads the shared
// RegressionSpec.Order as its time field; the deprecated params.time
// alias is removed (refused, naming order.field). The order spelling
// fits — output and derived seed — exactly what the alias fitted (the
// digest it pinned), and the removed key / panel conflicts are the
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

// TestMMMBayes_OrderIsTheTimeAlias: order {field: week} gives the
// removed alias's pinned digest under the derived seed (orderAsSeedTime)
// and under an explicit one.
func TestMMMBayes_OrderIsTheTimeAlias(t *testing.T) {
	t.Parallel()

	const tail = `"chains":1,"warmup":40,"draws":30}`
	for _, tc := range []struct {
		name   string
		params string
		seed   int64
		want   string
	}{
		{"order_only_derived_seed", `{"channels":["tv"],` + tail, 0, "9d823f85c561c3d2023f857786e2935d489237383144c5b4918ed4d9a4101642"},
		{"order_only_explicit_seed", `{"channels":["tv"],` + tail, 99, "e33f4e12579d6296ac8700f9229b3ed26516cafd7b3bbf7aa0f2bf6b79cd9879"},
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

// TestMMMBayes_OrderSpecRefusals: the removed params.time key (slot
// params.time, naming order.field), the panel rule (slot order) and an
// absent order (slot order) are PROCESSING_CONFIG, raised by the factory
// and by BayesSpecRefusal (predict's call) alike.
func TestMMMBayes_OrderSpecRefusals(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		params string
		order  *types.RegressionOrder
		slot   string
		reason string
	}{
		{"params_time_removed", `{"channels":["tv"],"time":"week"}`, nil, "params.time", reasonParamsTimeRemoved},
		{"params_time_removed_beside_order", `{"channels":["tv"],"time":"week"}`, &types.RegressionOrder{Field: "week"}, "params.time", reasonParamsTimeRemoved},
		{"partition_without_geo", `{"channels":["tv"]}`, &types.RegressionOrder{Field: "week", Partition: "market"}, "order", reasonOrderPartitionGeo},
		{"geo_without_partition", `{"channels":["tv"],"geo":"market"}`, &types.RegressionOrder{Field: "week"}, "order", reasonOrderPartitionGeo},
		{"geo_other_partition", `{"channels":["tv"],"geo":"market"}`, &types.RegressionOrder{Field: "week", Partition: "region"}, "order", reasonOrderPartitionGeo},
		{"no_time_at_all", `{"channels":["tv"]}`, nil, "order", ""},
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
			if tc.reason == reasonParamsTimeRemoved && (ce.Details["replacement"] != "order.field" || !strings.Contains(ce.Message, "order.field")) {
				t.Errorf("the removed-key refusal must name order.field: %s %v", ce.Message, ce.Details)
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
