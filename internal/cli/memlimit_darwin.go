//go:build darwin

package cli

import "golang.org/x/sys/unix"

// hostPhysicalMemory reads the installed RAM (sysctl hw.memsize) for
// the --memory-limit N% form.
func hostPhysicalMemory() (uint64, error) {
	return unix.SysctlUint64("hw.memsize")
}
