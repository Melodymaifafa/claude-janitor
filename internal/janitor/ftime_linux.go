//go:build linux

package janitor

import (
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// birthTime on Linux reads the creation time with statx(2) (kernel 4.11+).
// The legacy stat() struct carries no birth field, so statx is the only route
// (design.md §5, open item #3).
//
// Whether an answer comes back is a property of the *filesystem*, not the
// kernel: ext4 (256-byte inodes), xfs (v5), btrfs and overlayfs stacked on
// those fill STATX_BTIME; tmpfs, NFS and ancient ext4 layouts leave the mask
// bit clear. When the bit is clear -- or statx itself is unavailable (pre-4.11
// kernel, or a seccomp policy that blocks the syscall) -- we return the zero
// time and let the caller degrade. Verified on ext4 (btime present, and it
// stays put when the file is appended to) and on tmpfs (absent) -- see
// ftime_linux_test.go.
//
// AT_STATX_DONT_SYNC keeps a scan of hundreds of session files from forcing a
// server round trip on a network home directory; a creation time never changes
// after creation, so a cached answer is always the right answer.
func birthTime(path string, _ os.FileInfo) time.Time {
	var stx unix.Statx_t
	if err := unix.Statx(unix.AT_FDCWD, path, unix.AT_STATX_DONT_SYNC, unix.STATX_BTIME, &stx); err != nil {
		return time.Time{}
	}
	if stx.Mask&unix.STATX_BTIME == 0 {
		return time.Time{} // filesystem stores no creation time
	}
	return time.Unix(stx.Btime.Sec, int64(stx.Btime.Nsec))
}
