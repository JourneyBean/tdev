package mount

import (
	"context"
	"os/exec"
	"strconv"
	"strings"

	"tdev/internal/sshx"
)

func busyProcesses(ctx context.Context, path string) ([]int, error) {
	if _, err := exec.LookPath("lsof"); err != nil {
		return nil, nil
	}
	cmd := sshx.Command(ctx, "lsof", "-F", "p", "+f", "--", path)
	var output sshx.Buffer
	cmd.Stdout = &output
	_ = cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var pids []int
	for _, line := range strings.Split(output.String(), "\n") {
		if len(line) > 1 && line[0] == 'p' {
			if n, err := strconv.Atoi(line[1:]); err == nil && n > 0 {
				pids = append(pids, n)
			}
		}
	}
	return pids, nil
}
