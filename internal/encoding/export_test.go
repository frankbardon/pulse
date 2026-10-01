package encoding

// WritePreambleVersion exposes the unexported forced-version preamble
// writer to this directory's external (encoding_test) tests only, so they
// can build a 0x02 file before any 0x02 feature exists. Not part of the
// package API: production writers derive the version from schema content.
var WritePreambleVersion = writePreambleVersion
