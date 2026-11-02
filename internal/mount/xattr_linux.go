package mount

import "fmt"

func setAppleAttribute(string) error {
	return fmt.Errorf("Apple metadata probes are only supported on macOS")
}
