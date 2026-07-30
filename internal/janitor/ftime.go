package janitor

import (
	"os"
	"time"
)

// fileTimes carries the timestamps the janitor reads off a session file.
type fileTimes struct {
	mtime    time.Time // last write -- the idle signal (all classes)
	btime    time.Time // creation/birth time -- B-class pairing anchor
	hasBtime bool      // false when the OS/filesystem exposes no birth time
}

// statTimes returns mtime and birthtime for a path. mtime is portable via
// FileInfo.ModTime(); birthtime is per-OS (see ftime_*.go). Where birthtime is
// unavailable (e.g. some Linux filesystems) btime falls back to mtime and
// hasBtime is false -- callers must NOT pair by such a btime: transcripts are
// appended forever, so their mtime is "now", not creation time.
func statTimes(path string) (fileTimes, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return fileTimes{}, err
	}
	mt := fi.ModTime()
	bt := birthTime(fi) // per-OS; zero if unavailable
	has := !bt.IsZero()
	if !has {
		bt = mt
	}
	return fileTimes{mtime: mt, btime: bt, hasBtime: has}, nil
}
