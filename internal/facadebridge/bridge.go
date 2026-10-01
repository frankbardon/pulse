// Package facadebridge carries the few in-module hooks that sibling
// packages (the MCP go-sdk adapter) need from a *pulse.Pulse without the
// root facade exporting its engine handle. The root package installs
// every hook in an init function; this package imports nothing from
// the root, so there is no cycle.
package facadebridge

import "github.com/frankbardon/pulse/descriptor"

// ExtensionsSnapshot returns the read-only extension projection a
// *pulse.Pulse was built with. The argument is typed any because this
// package cannot import the root; passing anything other than a
// *pulse.Pulse returns nil. Installed by the root package's init.
var ExtensionsSnapshot func(p any) *descriptor.ExtensionsSnapshot
