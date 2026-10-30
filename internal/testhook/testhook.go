// Package testhook carries the in-module hooks black-box tests (package
// pulse_test, or any test package outside the root directory) need from
// a *pulse.Pulse without the root facade exporting its engine handle or
// its test-only switches. It is test-only in spirit: no production code
// reads it, and nothing outside the module can import it.
//
// The root package installs every hook in an init function in a non-test
// file (testhook_install.go), so a test binary built in any directory
// sees them. This package imports nothing from the root, so there is no
// import cycle; the *pulse.Pulse argument is therefore typed any.
package testhook

import "github.com/frankbardon/pulse/internal/service"

// Service returns the engine handle a *pulse.Pulse was built with, so a
// test can inspect extension-registry wiring (Extensions,
// ExtensionsSnapshot, InstanceSnapshot). Anything other than a non-nil
// *pulse.Pulse returns nil. Installed by the root package's init.
var Service func(p any) *service.Service

// ForcePanicRecoveryOff sets the root package's test-only override that
// makes every instance pulse.New builds afterwards behave as if
// Options.DisablePanicRecovery were set, so an engine panic crashes the
// test that hit it instead of surfacing as a recovered PROCESSING_PANIC
// error. It returns the previous setting. It writes a package-level
// variable unsynchronised: call it from TestMain before m.Run (or from a
// serial test that restores the previous value), never alongside
// parallel tests. Installed by the root package's init.
var ForcePanicRecoveryOff func(off bool) (prev bool)
