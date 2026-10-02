package service

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// TestShardParallel_HiddenSeriesOverlayRoutesAsUnknown: the parallel
// shard reducer folds SERIES overlays in the shared grouped tail
// (CanMergeRequest does not look at overlays, so a hidden kind fans out
// exactly as a never-registered one does). The tail must route the
// hidden kind through the instance registry: hidden ≡ never-registered
// after substitution, while the unscoped control computes the layer.
func TestShardParallel_HiddenSeriesOverlayRoutesAsUnknown(t *testing.T) {
	dict := encoding.NewDictionary()
	for _, v := range []string{"red", "green"} {
		_, _ = dict.Add(v)
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "color", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: dict},
		{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 1, CsvColumnIdx: 1},
	}}
	concat := [][]uint64{
		{0, math.Float64bits(10)}, {1, math.Float64bits(20)},
		{0, math.Float64bits(30)}, {1, math.Float64bits(40)},
	}
	shards := []struct {
		Name    string
		Records [][]uint64
	}{{Name: "a.pulse", Records: concat[0:2]}, {Name: "b.pulse", Records: concat[2:4]}}

	const hidden, never = string(types.OverlayKindIndexVsTotal), "OVERLAY_NEVER_REGISTERED"
	run := func(svc *Service, kind string) string {
		resp, err := svc.Process(context.Background(), &types.Request{
			Cohort:       &types.Cohort{Filename: "arch.pulse"},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "color"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "s"}},
			Overlays:     []types.OverlaySpec{{Kind: types.OverlayKind(kind), Scope: types.OverlayScopeGroup}},
		})
		if err != nil {
			var ce *errors.CodedError
			if stderrors.As(err, &ce) {
				b, _ := json.Marshal(map[string]any{"code": ce.Code, "message": ce.Message, "details": ce.Details})
				return string(b)
			}
			return "error: " + err.Error()
		}
		b, _ := json.Marshal(resp.Overlays)
		return "ok: " + string(b)
	}

	control, _ := setupShardArchive(t, "arch.pulse", schema, shards, concat)
	control.SetShardWorkers(2)
	scoped, _ := setupShardArchive(t, "arch.pulse", schema, shards, concat)
	scoped.SetShardWorkers(2)
	scoped.SetInstanceSnapshot(descx.NewInstanceSnapshot(nil, descx.FeatureSet{Hidden: []string{hidden}}))

	gotHidden, gotNever, open := run(scoped, hidden), run(scoped, never), run(control, hidden)
	if sub := strings.ReplaceAll(gotHidden, hidden, never); sub != gotNever {
		t.Errorf("hidden %s diverges from never-registered\nhidden: %s\nnever:  %s", hidden, gotHidden, gotNever)
	}
	if !strings.HasPrefix(open, "ok: ") {
		t.Fatalf("unscoped control failed; the case is vacuous: %s", open)
	}
}
