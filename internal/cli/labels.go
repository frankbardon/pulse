package cli

import (
	"fmt"
	"strings"

	"github.com/frankbardon/pulse/types"
)

// parseLabelBindings turns a slice of CLI --labels arguments into
// []*types.LabelBinding. Each arg has the form:
//
//	field=table             (mode defaults to replace)
//	field=table:replace
//	field=table:augment
//
// Whitespace around segments is trimmed. Duplicate fields surface
// here as a typed error so the CLI can reject the misuse before the
// pulse facade.
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
			return nil, fmt.Errorf("labels[%d]: field %q already bound at labels[%d]", i, field, prior)
		}
		seen[field] = i
		out = append(out, &types.LabelBinding{Field: field, Table: rest, Mode: mode})
	}
	return out, nil
}
