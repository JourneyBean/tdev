// Package reconnect defines retry policy shared by terminal and tunnel supervisors.
package reconnect

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
	"tdev/internal/protocol"
)

// Attempts counts additional attempts. -1 means always.
type Attempts int

func (a *Attempts) UnmarshalYAML(n *yaml.Node) error {
	v, err := ParseAttempts(n.Value)
	if err == nil {
		*a = Attempts(v)
	}
	return err
}
func (a Attempts) MarshalYAML() (any, error) {
	if a == -1 {
		return "always", nil
	}
	return int(a), nil
}
func ParseAttempts(s string) (int, error) {
	if s == "always" {
		return -1, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("attempts must be a nonnegative integer or always")
	}
	return n, nil
}

type Policy struct {
	Attempts Attempts      `yaml:"attempts"`
	Interval time.Duration `yaml:"interval"`
}
type Policies struct {
	Terminal Policy `yaml:"terminal"`
	Forward  Policy `yaml:"forward"`
	HTTP     Policy `yaml:"http"`
}

func Defaults() Policies {
	return Policies{Terminal: Policy{5, 2 * time.Second}, Forward: Policy{5, 2 * time.Second}, HTTP: Policy{-1, 5 * time.Second}}
}
func (p Policy) Validate() error {
	if p.Attempts < -1 || p.Interval <= 0 {
		return protocol.Fail("INVALID_CONFIG", 2, "invalid reconnect attempts or interval")
	}
	return nil
}
func Wait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type Budget struct {
	Policy Policy
	Used   int
}

func (b *Budget) Next(ctx context.Context) error {
	if b.Policy.Attempts >= 0 && b.Used >= int(b.Policy.Attempts) {
		return protocol.Fail("RETRIES_EXHAUSTED", 6, "reconnect attempts exhausted after %d retries", b.Used)
	}
	b.Used++
	return Wait(ctx, b.Policy.Interval)
}

// Healthy is called only after entering the requested operation, never after a probe or TCP handshake.
// A minimum stable interval prevents a rapid connect/disconnect loop from resetting the budget.
func (b *Budget) Healthy(duration time.Duration) {
	threshold := b.Policy.Interval
	if threshold < time.Second {
		threshold = time.Second
	}
	if duration >= threshold {
		b.Used = 0
	}
}
func Retryable(err error) bool {
	if err == nil {
		return false
	}
	e := protocol.Normalize(err)
	if e.Code != "CONNECTION_FAILED" {
		return false
	}
	for _, s := range []string{"Permission denied", "Host key verification failed", "REMOTE HOST IDENTIFICATION HAS CHANGED", "Too many authentication failures", "Bad configuration", "no matching host key", "no matching key exchange", "bad permissions", "forwarding failed", "cannot listen", "Address already in use", "administratively prohibited"} {
		if strings.Contains(e.Message, s) {
			return false
		}
	}
	for _, s := range []string{"Connection timed out", "Operation timed out", "Connection refused", "Connection reset", "Connection closed", "closed by remote host", "No route to host", "Network is unreachable", "Temporary failure in name resolution", "Could not resolve hostname", "nodename nor servname provided", "Broken pipe", "context deadline exceeded"} {
		if strings.Contains(e.Message, s) {
			return true
		}
	}
	return false
}
