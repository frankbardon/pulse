package skills

import (
	"strings"
	"testing"
)

// orderByClaims returns the balanced parenthetical that follows each
// "`order_by` (" in body — the span where an op-win-* skill states what
// an order_by key admits.
func orderByClaims(body string) []string {
	const marker = "`order_by` ("
	var out []string
	for rest := body; ; {
		i := strings.Index(rest, marker)
		if i < 0 {
			return out
		}
		rest = rest[i+len(marker):]
		depth, end := 1, -1
		for j, r := range rest {
			switch r {
			case '(':
				depth++
			case ')':
				depth--
			}
			if depth == 0 {
				end = j
				break
			}
		}
		if end < 0 {
			out = append(out, rest)
			return out
		}
		out = append(out, rest[:end])
	}
}

// TestWindowSkillOrderByClaimsMatchEngine: an order_by key admits every
// field type except set_* (PULSE_WINDOW_INVALID) and vec_*
// (PULSE_VECTOR_FIELD_NOT_SCALAR) — pinned against predict and the
// runtime by the root TestWindowOrderBy_AdmitsAllButSetAndVector. No
// op-win-* skill may narrow it to "numeric" again, and one that states
// a type rule must name both refused families.
func TestWindowSkillOrderByClaimsMatchEngine(t *testing.T) {
	checked := 0
	for _, stem := range embeddedMarkdownNames(t) {
		if !strings.HasPrefix(stem, "op-win-") {
			continue
		}
		raw, ok := Get(stem)
		if !ok {
			t.Errorf("%s: not loadable via Get", stem)
			continue
		}
		for _, claim := range orderByClaims(StripFrontmatter(raw)) {
			checked++
			if strings.Contains(claim, "numeric") {
				t.Errorf("%s: order_by claim %q says numeric; every type but set_* and vec_* is admitted", stem, claim)
			}
			if strings.Contains(claim, "type") &&
				(!strings.Contains(claim, "`set_*`") || !strings.Contains(claim, "`vec_*`")) {
				t.Errorf("%s: order_by claim %q states a type rule without naming both `set_*` and `vec_*`", stem, claim)
			}
		}
	}
	if checked == 0 {
		t.Fatal("found no order_by claim in any op-win-* skill; the parser is broken")
	}
}
