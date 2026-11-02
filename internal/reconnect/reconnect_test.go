package reconnect

import (
	"context"
	"go.yaml.in/yaml/v3"
	"testing"
	"time"
)

func TestPolicyAndBudgets(t *testing.T) {
	for _, tc := range []struct {
		s  string
		n  int
		ok bool
	}{{"0", 0, true}, {"2", 2, true}, {"always", -1, true}, {"-1", 0, false}, {"bad", 0, false}} {
		n, e := ParseAttempts(tc.s)
		if (e == nil) != tc.ok || (tc.ok && n != tc.n) {
			t.Fatal(tc, n, e)
		}
	}
	for _, n := range []Attempts{0, 1, 3} {
		b := Budget{Policy: Policy{n, time.Nanosecond}}
		for i := 0; i < int(n); i++ {
			if e := b.Next(context.Background()); e != nil {
				t.Fatal(e)
			}
		}
		if e := b.Next(context.Background()); e == nil {
			t.Fatal("exceeded retry limit")
		}
	}
}
func TestCancellationAndStableReset(t *testing.T) {
	b := Budget{Policy: Policy{1, time.Hour}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if b.Next(ctx) == nil || time.Since(start) > time.Second {
		t.Fatal("cancellation did not interrupt timer")
	}
	b = Budget{Policy: Policy{3, 10 * time.Millisecond}, Used: 2}
	b.Healthy(time.Millisecond)
	if b.Used != 2 {
		t.Fatal("rapid disconnect reset the budget")
	}
	b.Healthy(time.Second)
	if b.Used != 0 {
		t.Fatal("healthy attachment did not reset outage counter")
	}
	b.Policy.Attempts = -1
	for range 8 {
		if err := b.Next(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}
func TestYAML(t *testing.T) {
	p := Defaults()
	if err := yaml.Unmarshal([]byte("terminal:\n  attempts: always\n  interval: 25ms\n"), &p); err != nil {
		t.Fatal(err)
	}
	if p.Terminal.Attempts != -1 || p.Terminal.Interval != 25*time.Millisecond || p.Forward.Attempts != 5 {
		t.Fatal(p)
	}
}
