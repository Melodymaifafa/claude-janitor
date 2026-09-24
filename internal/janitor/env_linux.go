//go:build linux

package janitor

import (
	"fmt"
	"os"
	"strings"
)

// procEnviron reads another process's environment from /proc/<pid>/environ,
// which the kernel exposes NUL-separated to the process owner (and to root).
// A process owned by another user gives EACCES, which surfaces as "no
// evidence" and never as a kill.
func procEnviron(pid int32) (map[string]string, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		return nil, err
	}
	env := make(map[string]string, 48)
	for _, s := range strings.Split(string(b), "\x00") {
		if s == "" {
			continue
		}
		if k, v, ok := strings.Cut(s, "="); ok {
			env[k] = v
		}
	}
	return env, nil
}
