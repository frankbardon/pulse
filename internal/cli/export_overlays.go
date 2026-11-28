package cli

import (
	"bytes"
	"encoding/json"
	"fmt"

	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/jsonfinite"
	"github.com/frankbardon/pulse/internal/pathq"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// exportOverlaysFlagUsage is the export leaves' --overlays flag.
const exportOverlaysFlagUsage = "Embed executed overlay layers in the export (io.ExportJob.Overlays): a JSON file holding a --json result envelope, a result object with an overlays array (a process or compose response), or the bare array. Layers are results, not request overlay specs; NDJSON, Arrow, Parquet and Excel embed them, CSV, TSV and SPSS skip them with PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED."

// loadOverlayLayers reads the executed overlay layers --overlays names.
//
// The file is a result, so it decodes with the undefined-figure rule
// (jsonfinite: a null figure stays NaN, never 0), the way `pulse explain
// --response` reads one; a --json envelope is unwrapped to its data and
// a result object to its `overlays`. Each layer decodes strictly: a
// request's overlay spec (`reference`, `targets`, `params`) is no layer
// and is refused rather than embedded as an empty one, as is a layer
// with no kind or payload shape. Every refusal is DATA_FILE naming the
// file (details.path).
func loadOverlayLayers(fs afero.Fs, path string) ([]*types.OverlayLayer, error) {
	data, err := afero.ReadFile(fs, path)
	if err != nil {
		return nil, overlayFileError(path, "cannot be read: "+err.Error())
	}
	var env struct {
		FormatVersion *string         `json:"format_version"`
		Data          json.RawMessage `json:"data"`
	}
	if json.Unmarshal(data, &env) == nil && env.FormatVersion != nil && len(env.Data) > 0 {
		data = env.Data
	}
	if trimmed := bytes.TrimSpace(data); len(trimmed) > 0 && trimmed[0] == '{' {
		var res struct {
			Overlays json.RawMessage `json:"overlays"`
		}
		if err := json.Unmarshal(trimmed, &res); err != nil {
			return nil, overlayFileError(path, "is not JSON: "+err.Error())
		}
		if len(res.Overlays) == 0 {
			return nil, overlayFileError(path, "holds no `overlays` array (pass a result whose request ran overlays, or the bare array)")
		}
		data = res.Overlays
	}
	strict := json.NewDecoder(bytes.NewReader(data))
	strict.DisallowUnknownFields()
	var probe []*types.OverlayLayer
	if err := strict.Decode(&probe); err != nil {
		return nil, overlayFileError(path, "does not hold executed overlay layers (a result's `overlays`, not request overlay specs): "+err.Error())
	}
	var layers []*types.OverlayLayer
	if err := jsonfinite.Unmarshal(data, &layers); err != nil {
		return nil, overlayFileError(path, "is not JSON: "+err.Error())
	}
	for i, l := range layers {
		if l == nil || l.Kind == "" || l.Payload.Shape == "" {
			return nil, overlayFileError(path, fmt.Sprintf("overlay layer %d has no kind or payload shape: a layer is an executed result, not a request spec", i))
		}
	}
	return layers, nil
}

func overlayFileError(path, msg string) error {
	return perrors.NewCodedErrorWithDetails(perrors.DATA_FILE,
		"--overlays "+pathq.Quote(path)+" "+msg, map[string]any{"path": path, "flag": "overlays"})
}
