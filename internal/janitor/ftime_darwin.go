//go:build darwin

package janitor

import (
	"os"
	"syscall"
	"time"
)

// birthTime reads st_birthtimespec on macOS (VERIFIED: Mac files carry it).
func birthTime(fi os.FileInfo) time.Time {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return time.Time{}
	}
	return time.Unix(st.Birthtimespec.Sec, st.Birthtimespec.Nsec)
}
