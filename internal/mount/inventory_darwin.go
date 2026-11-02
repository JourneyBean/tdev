package mount

import (
	"fmt"
	"golang.org/x/sys/unix"
)

func Inventory() ([]Entry, error) {
	n, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
	if err != nil {
		return nil, err
	}
	stats := make([]unix.Statfs_t, n+16)
	n, err = unix.Getfsstat(stats, unix.MNT_NOWAIT)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, n)
	for _, s := range stats[:n] {
		out = append(out, Entry{Path: unix.ByteSliceToString(s.Mntonname[:]), Source: unix.ByteSliceToString(s.Mntfromname[:]), Type: unix.ByteSliceToString(s.Fstypename[:]), ID: fmt.Sprint(s.Fsid)})
	}
	return out, nil
}
