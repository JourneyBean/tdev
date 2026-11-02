package transfer

import (
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"runtime"
)

// Check aliases without rewriting filenames: Linux may legitimately store both NFC and NFD names.
// macOS destinations conservatively reject case/normalization aliases before any filesystem writes.
func checkAliases(m Manifest, goos string) error {
	if goos != "darwin" {
		return nil
	}
	seen := map[string]string{}
	fold := cases.Fold()
	for _, e := range m.Entries {
		key := fold.String(norm.NFC.String(e.Path))
		if prior, ok := seen[key]; ok && prior != e.Path {
			return fail("destination cannot safely distinguish Unicode/case aliases %q and %q", prior, e.Path)
		}
		seen[key] = e.Path
	}
	return nil
}
func nativeAliases(m Manifest) error { return checkAliases(m, runtime.GOOS) }
