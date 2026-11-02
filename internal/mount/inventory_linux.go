package mount

import (
	"bufio"
	"os"
	"strings"
)

func unescape(s string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(s)
}
func Inventory() ([]Entry, error) {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Entry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		s := strings.SplitN(scanner.Text(), " - ", 2)
		if len(s) != 2 {
			continue
		}
		left, right := strings.Fields(s[0]), strings.Fields(s[1])
		if len(left) < 6 || len(right) < 2 {
			continue
		}
		out = append(out, Entry{Path: unescape(left[4]), Source: unescape(right[1]), Type: right[0], ID: left[0] + ":" + left[2]})
	}
	return out, scanner.Err()
}
