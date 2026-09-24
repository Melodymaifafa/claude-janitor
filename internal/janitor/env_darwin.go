//go:build darwin

package janitor

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"

	"golang.org/x/sys/unix"
)

// procEnviron reads another process's environment block on macOS.
//
// gopsutil's Environ() is "not implemented yet" on darwin (MEASURED 2026-09-24:
// 19/19 live claude processes returned that error), so this reads the raw
// KERN_PROCARGS2 sysctl instead. It needs no elevation for processes owned by
// the same user, which is the only case that matters: the janitor kills the
// sessions of the user it runs as. Processes owned by another user return
// EINVAL/EPERM, which surfaces as "no evidence" and never as a kill.
//
// Layout of the KERN_PROCARGS2 buffer (xnu sysctl.c):
//
//	int32 argc | exec_path \0 | \0 padding | argv[0..argc-1] each \0 | env each \0 | \0
func procEnviron(pid int32) (map[string]string, error) {
	buf, err := unix.SysctlRaw("kern.procargs2", int(pid))
	if err != nil {
		return nil, err
	}
	if len(buf) < 4 {
		return nil, errors.New("procargs2: short buffer")
	}
	argc := int(binary.LittleEndian.Uint32(buf[:4]))
	rest := buf[4:]

	// exec_path, then its NUL padding to the next word boundary.
	i := bytes.IndexByte(rest, 0)
	if i < 0 {
		return nil, errors.New("procargs2: no exec path")
	}
	rest = rest[i:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}

	// Skip exactly argc argv strings; whatever follows is the environment.
	for a := 0; a < argc; a++ {
		j := bytes.IndexByte(rest, 0)
		if j < 0 {
			return nil, errors.New("procargs2: truncated argv")
		}
		rest = rest[j+1:]
	}

	env := make(map[string]string, 48)
	for len(rest) > 0 {
		j := bytes.IndexByte(rest, 0)
		if j < 0 {
			break
		}
		s := string(rest[:j])
		rest = rest[j+1:]
		if s == "" {
			break // the terminating empty string ends the block
		}
		if k, v, ok := strings.Cut(s, "="); ok {
			env[k] = v
		}
	}
	return env, nil
}
