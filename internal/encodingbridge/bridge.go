// Package encodingbridge carries the few unexported helpers of the
// public encoding package that its internal twin (internal/encoding)
// needs, without the public package exporting them. The public package
// installs every hook in an init function; this package imports nothing
// from the module, so there is no cycle (encoding -> encodingbridge,
// internal/encoding -> encoding + encodingbridge).
//
// Schema-taking hooks are typed any because this package cannot import
// encoding; internal/encoding wraps each one in a typed helper, and
// passing anything other than a *encoding.Schema panics.
package encodingbridge

import "io"

var (
	// WriteHeaderVersion writes a 9-byte header declaring version v.
	WriteHeaderVersion func(w io.Writer, v byte) error

	// WriteSchemaVersion writes s's schema block in version v's layout
	// (the 0x02 extension block included).
	WriteSchemaVersion func(w io.Writer, s any, v byte) error

	// ValidateGroupShape is Schema.ValidateGroups without the group
	// dictionary checks.
	ValidateGroupShape func(s any) error

	// MemberSets returns, per logical field, the owning group (or -1)
	// and the member slot within it.
	MemberSets func(s any) (groupOf, slotOf []int)

	// GroupEntryGeometry returns group g's entry width and the offset of
	// the member null bitmap within an entry (-1 when no member is
	// nullable).
	GroupEntryGeometry func(s any, g int) (width, bmOff int)

	// GroupDescriptorBytes is the on-wire size of one group's descriptor
	// in the 0x02 groups section, for a group of the given member count.
	GroupDescriptorBytes func(members int) int
)
