// Package featstream is the one spec-level streaming verdict for the
// built-in FEAT_* operators, shared by the engine's streaming gate
// (internal/processing/feature IsStreamableWithExt / BuildStreamingWithExt)
// and predict's streamable reasons (internal/descriptor computeStreamable),
// so the two cannot disagree.
//
// The streaming feature drive runs every feature's pre-pass over the RAW
// record (pass 1) before any feature has emitted (pass 2). A global-pass
// operator whose pre-pass reads a column an earlier feature derives sees
// it absent there and would fold nothing — every value null or every
// quantile cut at zero — while the buffered path, which applies features
// in order, computes it. Such a spec routes buffered:
//
//   - FEAT_TARGET_ENCODE: its field, target or any name params.fit_on
//     reads (internal/targetencode.Streamable);
//   - FEAT_BUCKETIZE in quantile mode: its field.
//
// The other built-ins either read only stored levels (FEAT_FREQUENCY_ENCODE
// and FEAT_TRAIN_TEST_SPLIT read categorical / packed_bool levels, which a
// derived numeric column never carries — null on both arms alike) or are
// row-local (their pre-pass is a no-op and pass 2 emits in order).
//
// Leaf: the public encoding / types packages + targetencode + paramread
// (a Probe: the operator itself decodes and records its params).
package featstream

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/targetencode"
	"github.com/frankbardon/pulse/types"
)

// Streamable reports whether feat can run on the streaming feature drive
// over schema; when it cannot, reason says why (predict's
// streamable_reasons entry). Params that do not decode leave the verdict
// to the operator's own refusal (true here). A nil schema stores no
// field.
func Streamable(feat *types.Feature, schema *encoding.Schema) (bool, string) {
	if feat == nil {
		return true, ""
	}
	stored := func(name string) bool { return schema != nil && schema.Field(name) != nil }
	switch feat.Type {
	case types.FEAT_TARGET_ENCODE:
		var p targetencode.Params
		if len(feat.Params) > 0 && paramread.Probe(feat.Params, &p) != nil {
			return true, ""
		}
		if !targetencode.Streamable(feat.Field, p, schema) {
			return false, "feature FEAT_TARGET_ENCODE reads a column an earlier feature derives (field, target or fit_on), which the streaming pre-pass does not see"
		}
	case types.FEAT_BUCKETIZE:
		var p struct {
			Quantiles int `json:"quantiles"`
		}
		if len(feat.Params) > 0 && paramread.Probe(feat.Params, &p) != nil {
			return true, ""
		}
		if p.Quantiles > 0 && feat.Field != "" && !stored(feat.Field) {
			return false, "feature FEAT_BUCKETIZE cuts quantiles of a column an earlier feature derives, which the streaming pre-pass does not see"
		}
	}
	return true, ""
}
