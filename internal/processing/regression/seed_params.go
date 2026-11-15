package regression

import (
	"bytes"
	"encoding/json"

	"github.com/frankbardon/pulse/internal/regresample"
	"github.com/frankbardon/pulse/types"
)

// seedNeutralParams are, per regression type, the params keys that
// shape scoring only and never the fit, so they never enter the
// rng_seed 0 derivation: REG_MMM_HILL's params.carryover decides how a
// holdout or saved-model score carries the adstock state, the refits
// never read it — spelling it out (true or false) draws the replicates
// leaving it out draws, as REG_MMM_BAYES keeps it out of its own draws'
// seed (internal/regmmm outputOnlyParams).
var seedNeutralParams = map[types.RegressionType][]string{
	types.REG_MMM_HILL: {"carryover"},
}

// seedParams is raw (already regresample.CanonicalJSON) with typ's
// seed-neutral keys removed, re-canonicalised; raw itself when it
// carries none, so a spec without them hashes as it always has.
func seedParams(typ types.RegressionType, raw json.RawMessage) json.RawMessage {
	keys := seedNeutralParams[typ]
	if len(keys) == 0 || len(raw) == 0 {
		return raw
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return raw
	}
	found := false
	for _, k := range keys {
		if _, ok := obj[k]; ok {
			delete(obj, k)
			found = true
		}
	}
	if !found {
		return raw
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if enc.Encode(obj) != nil {
		return raw
	}
	return regresample.CanonicalJSON(bytes.TrimSpace(buf.Bytes()))
}
