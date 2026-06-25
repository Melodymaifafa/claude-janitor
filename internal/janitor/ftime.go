package janitor

import (
	"os"
	"time"
)

// fileTimes carries the two timestamps the janitor reads off a session file.
type fileTimes struct {
	mtime time.Time // last write -- the idle signal (all classes)
	btime time.Time // creation/birth time -- B-class reverse-match anchor
}

// statTimes returns mtime and birthtime for a path. mtime is portable via
// FileInfo.ModTime(); birthtime is per-OS (see ftime_*.go). Where birthtime is
// unavailable (e.g. some Linux filesystems), btime falls back to mtime -- safe
// for an idle json written once at creation, where mtime approximate creation
// time (design.md §5).
func statTimes(path string) (fileTimes, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return fileTimes{}, err
	}
	mt := fi.ModTime()
	bt := birthTime(fi) // per-OS; zero if unavailable
	if bt.IsZero() {
		bt = mt
	}
	return fileTimes{mtime: mt, btime: bt}, nil
}
