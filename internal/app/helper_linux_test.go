//go:build linux

package app

import "golang.org/x/sys/unix"

func enableTestSubreaper() bool { return unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0) == nil }
