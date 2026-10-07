package descriptor

import (
	"encoding/json"
	"sync"

	"github.com/frankbardon/pulse/internal/returnplan"
	"github.com/frankbardon/pulse/types"
)

// return_plan_cache.go memoizes the Response-rooted `return` resolution
// per instance (PRD FR-42, #219). resolveReturnBlock walks the Response
// type reflectively on every call; measured on a small Process
// (BenchmarkProcessDefaultReturn, root package) the walk cost several
// times the run itself, and a request resolves it more than once (the
// facade's shaping and Service.process' compute plan). The resolved
// selection is a pure function of the effective block and the
// instance's feature visibility — both fixed for a key on one snapshot —
// so the plan is resolved once per distinct block and shared.
//
// What is NEVER cached: Plan.Exact, the REQUEST-DERIVED precision
// exemptions (a count aggregation's data column, a count crosstab
// cell). The cache stores the plan before ResolveReturn derives Exact,
// and every hit hands back a fresh shallow copy, so a request's Exact
// lands on its own copy and two requests never share one. Plans are
// read-only after resolution (no caller mutates Include / Exclude /
// Keep), so the copies share those slices safely. Refusals are not
// cached: an invalid block re-resolves and re-raises its own error.

// returnPlanCacheLimit bounds the distinct blocks one instance keeps:
// the instance default, the MCP-injected preset and a handful of
// recurring request blocks fit; past it a block simply resolves
// uncached, so an adversarial stream of distinct includes cannot grow
// memory.
const returnPlanCacheLimit = 64

type returnPlanCache struct {
	mu    sync.RWMutex
	plans map[string]*returnplan.Plan
}

func newReturnPlanCache() *returnPlanCache {
	return &returnPlanCache{plans: map[string]*returnplan.Plan{}}
}

// resolveResponseReturn is resolveReturnBlock over the Response root,
// memoized on inst (uncached on a nil snapshot). The result is the
// caller's own copy; its Exact is always nil.
func resolveResponseReturn(ret *types.Return, inst *InstanceSnapshot) (*returnplan.Plan, error) {
	var c *returnPlanCache
	if inst != nil {
		c = inst.returnPlans
	}
	if c == nil {
		return resolveReturnBlock(ret, returnRoot, inst)
	}
	raw, err := json.Marshal(ret)
	if err != nil {
		return resolveReturnBlock(ret, returnRoot, inst)
	}
	key := string(raw)
	c.mu.RLock()
	cached := c.plans[key]
	c.mu.RUnlock()
	if cached != nil {
		cp := *cached
		return &cp, nil
	}
	plan, err := resolveReturnBlock(ret, returnRoot, inst)
	if err != nil {
		return nil, err
	}
	stored := *plan
	stored.Exact = nil
	c.mu.Lock()
	if len(c.plans) < returnPlanCacheLimit {
		c.plans[key] = &stored
	}
	c.mu.Unlock()
	return plan, nil
}
