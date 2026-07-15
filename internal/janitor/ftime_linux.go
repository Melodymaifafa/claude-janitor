//go:build linux

package janitor

import (
	"os"
	"time"
)

// birthTime on Linux: btime is only exposed via statx on some filesystems
// (ext4/xfs) and not at all on others; the legacy stat() struct has no birth
// field. Per design.md §5 (open item #3) we return zero here and let the caller
// fall back to mtime, which is safe for an idle json written once at creation.
// A statx-based refinement is on-box follow-up, not a blocker.
func birthTime(_ os.FileInfo) time.Time {
	return time.Time{}
}
