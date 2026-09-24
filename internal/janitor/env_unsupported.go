//go:build !darwin && !linux

package janitor

import "errors"

// errNoEnviron marks the platforms where another process's environment is not
// readable with the access the janitor has. Windows keeps the environment block
// inside the target process's PEB: reading it needs PROCESS_VM_READ plus a
// bitness-matched PEB walk, and gopsutil does not implement Environ() there
// either. The claim route is therefore unavailable on Windows and the desktop
// scan falls back to the working-directory filter and the timestamp window,
// which is exactly the behaviour every platform had before.
var errNoEnviron = errors.New("reading another process's environment is not supported on this platform")

func procEnviron(int32) (map[string]string, error) { return nil, errNoEnviron }
