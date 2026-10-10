package jsonshared

import (
	"fmt"

	"github.com/frankbardon/pulse/errors"
)

// LateKeys is the iocore.LateKeyRefuser state the ndjson and jsonarray
// readers share. Both take their column set from the FIRST object's
// keys; when armed, a later object carrying a key outside that set is
// refused with PULSE_IMPORT_SCHEMA_KEY_LATE instead of being dropped.
// The zero value is disarmed — the inference path's behaviour.
type LateKeys struct {
	on     bool
	header map[string]struct{}
}

// Set arms or disarms the refusal. It survives a reader Reset.
func (l *LateKeys) Set(on bool) { l.on = on }

// Forget drops the cached header set; the next Check rebuilds it. Call
// it whenever the reader's header is cleared.
func (l *LateKeys) Forget() { l.header = nil }

// Check returns the coded refusal for the first key in keys (object
// order) that header does not carry, or nil. row is the 1-based data
// row; extra is merged into the details (ndjson adds its line number).
// A disarmed LateKeys never refuses.
func (l *LateKeys) Check(keys, header []string, row int, extra map[string]any) error {
	if !l.on {
		return nil
	}
	if l.header == nil {
		l.header = make(map[string]struct{}, len(header))
		for _, h := range header {
			l.header[h] = struct{}{}
		}
	}
	for _, k := range keys {
		if _, ok := l.header[k]; ok {
			continue
		}
		details := map[string]any{
			"column":  k,
			"row":     row,
			"columns": append([]string(nil), header...),
		}
		for dk, dv := range extra {
			details[dk] = dv
		}
		return errors.NewCodedErrorWithDetails(
			errors.PULSE_IMPORT_SCHEMA_KEY_LATE,
			fmt.Sprintf("row %d: key %q is not among the first object's keys, which define the columns (pass --ignore-extra to drop it)", row, k),
			details,
		)
	}
	return nil
}
