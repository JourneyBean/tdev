package mount

import "golang.org/x/sys/unix"

func setAppleAttribute(path string) error {
	return unix.Setxattr(path, "com.apple.tdev.probe", []byte("metadata guard probe"), 0)
}
