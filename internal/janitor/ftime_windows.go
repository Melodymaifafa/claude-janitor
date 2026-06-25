//go:build windows

package janitor

import (
	"os"
	"syscall"
	"time"
)

// birthTime reads CreationTime on Windows (natively available; design.md §5).
func birthTime(fi os.FileInfo) time.Time {
	st, ok := fi.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return time.Time{}
	}
	return time.Unix(0, st.CreationTime.Nanoseconds())
}
