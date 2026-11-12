//go:build linux

package cli

import "golang.org/x/sys/unix"

// hostPhysicalMemory reads the installed RAM (sysinfo totalram × unit)
// for the --memory-limit N% form. It is the host's (or VM's) RAM, not a
// container cgroup limit.
func hostPhysicalMemory() (uint64, error) {
	var si unix.Sysinfo_t
	if err := unix.Sysinfo(&si); err != nil {
		return 0, err
	}
	return uint64(si.Totalram) * uint64(si.Unit), nil
}
