// Package facadebridge carries the few in-module hooks that sibling
// packages (the MCP go-sdk adapter) need from a *pulse.Pulse without the
// root facade exporting its engine handle. The root package installs
// every hook in an init function; this package imports nothing from
// the root, so there is no cycle.
package facadebridge

import descx "github.com/frankbardon/pulse/internal/descriptor"

// ExtensionsSnapshot returns the read-only extension projection a
// *pulse.Pulse was built with. The argument is typed any because this
// package cannot import the root; passing anything other than a
// *pulse.Pulse returns nil. Installed by the root package's init.
var ExtensionsSnapshot func(p any) *descx.ExtensionsSnapshot

// CohortScanDisabled reports whether the feature profile a *pulse.Pulse
// was built with sets behaviour.disable_cohort_scan. The MCP adapter ORs
// it into its own Config.DisableCohortScan. Anything other than a
// *pulse.Pulse returns false. Installed by the root package's init.
var CohortScanDisabled func(p any) bool

// InstanceSnapshot returns the no-execute instance view (resolved
// feature set + extension projection) a *pulse.Pulse was built with.
// The MCP adapter consults it to register only the tools, prompts and
// resources the instance offers. A profile-free Pulse returns an
// unscoped snapshot (every name enabled); anything other than a
// *pulse.Pulse returns nil, which is also unscoped. Installed by the
// root package's init.
var InstanceSnapshot func(p any) *descx.InstanceSnapshot
