package cli

import (
	"fmt"
	"strings"

	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// parseLabelBindings turns a slice of CLI --labels arguments into
// []*types.LabelBinding. Each arg has the form:
//
//	field=table             (mode defaults to replace)
//	field=table:replace
//	field=table:augment
//
// Whitespace around segments is trimmed. A duplicate field surfaces
// here as PULSE_LABEL_DUPLICATE_BINDING — the code the library raises
// for the same misuse — so the CLI rejects it before the pulse facade,
// on every leaf alike (export and `export predict` included).
func parseLabelBindings(args []string) ([]*types.LabelBinding, error) {
	out := make([]*types.LabelBinding, 0, len(args))
	seen := map[string]int{}
	for i, raw := range args {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return nil, fmt.Errorf("labels[%d]: empty binding", i)
		}
		eq := strings.IndexByte(raw, '=')
		if eq <= 0 || eq == len(raw)-1 {
			return nil, fmt.Errorf("labels[%d]: expected field=table[:mode]; got %q", i, raw)
		}
		field := strings.TrimSpace(raw[:eq])
		rest := strings.TrimSpace(raw[eq+1:])
		mode := types.LabelModeReplace
		if colon := strings.IndexByte(rest, ':'); colon >= 0 {
			tableName := strings.TrimSpace(rest[:colon])
			modeStr := strings.TrimSpace(rest[colon+1:])
			switch modeStr {
			case "", "replace":
				mode = types.LabelModeReplace
			case "augment":
				mode = types.LabelModeAugment
			default:
				return nil, fmt.Errorf("labels[%d]: unknown mode %q (expected replace or augment)", i, modeStr)
			}
			rest = tableName
		}
		if field == "" || rest == "" {
			return nil, fmt.Errorf("labels[%d]: field and table are required (%q)", i, raw)
		}
		if prior, ok := seen[field]; ok {
			return nil, perrors.NewCodedErrorWithDetails(perrors.PULSE_LABEL_DUPLICATE_BINDING,
				fmt.Sprintf("labels[%d]: field %q already bound at labels[%d]", i, field, prior),
				map[string]any{"field": field, "index": i, "prior": prior, "option": "--labels"})
		}
		seen[field] = i
		out = append(out, &types.LabelBinding{Field: field, Table: rest, Mode: mode})
	}
	return out, nil
}
