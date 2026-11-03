package main

import (
	"encoding/json"
	"strings"
)

// jsonEsc escapes s for splicing between the quotes of a JSON string
// literal. Request bodies built by concatenation carry OS paths, and a
// Windows path's backslashes (C:\Users\...) are invalid JSON escapes
// unless escaped.
func jsonEsc(s string) string {
	b, _ := json.Marshal(s)
	return strings.TrimSuffix(strings.TrimPrefix(string(b), `"`), `"`)
}
