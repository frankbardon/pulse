package encoding

// DateDays interprets a raw `date` word — as returned by ReadFieldValue
// or ParseDate — as its signed epoch-day count. The on-wire date word is
// two's-complement int32 days since 1970-01-01, so 1969-12-31 is -1 and
// 1900-01-01 is -25567. Zero-extending the word instead (uint64(raw))
// turns every pre-1970 date into a day near 4.29e9; always read a date
// through this helper.
func DateDays(raw uint64) int32 {
	return int32(uint32(raw))
}

// DateTimeSeconds interprets a raw `datetime` word — as returned by
// ReadFieldValue or ParseDateTime — as its signed epoch-second count. The
// on-wire datetime word is two's-complement int64 seconds since
// 1970-01-01T00:00:00Z, so 1969-12-31T23:59:59Z is -1.
func DateTimeSeconds(raw uint64) int64 {
	return int64(raw)
}
