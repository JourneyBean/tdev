package cli

import (
	"context"
	"reflect"
	"testing"

	"tdev/internal/config"
)

func overrideFixture(t *testing.T) config.Project {
	t.Helper()
	store := config.Store{Root: t.TempDir()}
	p, err := store.Create(context.Background(), "demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p.Forwards = []config.Forward{{ID: "api", Direction: "L", BindAddress: "127.0.0.1", ListenPort: 3000, TargetHost: "localhost", TargetPort: 8000}}
	return p
}

func TestMergeLocalPortsPreservesFreshRemoteDefinition(t *testing.T) {
	p := overrideFixture(t)
	port := 8080
	o := projectOverride{ProjectID: p.ID, Forwards: map[string]forwardOverride{"api": {ListenPort: &port}}}
	merged, err := mergeProject(p, o)
	if err != nil {
		t.Fatal(err)
	}
	if merged.Forwards[0].ListenPort != 8080 || p.Forwards[0].ListenPort != 3000 {
		t.Fatal("mutated remote definition")
	}
	p.Forwards[0].TargetPort = 9000
	p.Windows[0].Name = "新窗口_日本語_🙂"
	p.Revision++
	merged, err = mergeProject(p, o)
	if err != nil {
		t.Fatal(err)
	}
	if merged.Forwards[0].ListenPort != 8080 || merged.Forwards[0].TargetPort != 9000 || !reflect.DeepEqual(merged.Windows, p.Windows) {
		t.Fatal("overrides hid fresh remote configuration")
	}
	o.Forwards["api"] = forwardOverride{Disabled: true}
	merged, err = mergeProject(p, o)
	if err != nil || len(merged.Forwards) != 0 {
		t.Fatal(merged, err)
	}
}

func TestMergeRejectsStaleMissingReverseAndCollisions(t *testing.T) {
	p := overrideFixture(t)
	port := 8080
	o := projectOverride{ProjectID: config.NewID(), Forwards: map[string]forwardOverride{"api": {ListenPort: &port}}}
	if _, err := mergeProject(p, o); err == nil {
		t.Fatal("accepted stale identity")
	}
	o.ProjectID = p.ID
	o.Forwards = map[string]forwardOverride{"gone": {ListenPort: &port}}
	if _, err := mergeProject(p, o); err == nil {
		t.Fatal("accepted deleted remote mapping")
	}
	o.Forwards = map[string]forwardOverride{"api": {ListenPort: &port}}
	p.Forwards[0].Direction = "R"
	if _, err := mergeProject(p, o); err == nil {
		t.Fatal("overrode remote listener")
	}
	p.Forwards[0].Direction = "L"
	o.LocalForwards = []config.Forward{{ID: "other", Direction: "L", BindAddress: "127.0.0.1", ListenPort: 8080, TargetHost: "localhost", TargetPort: 9000}}
	if _, err := mergeProject(p, o); err == nil {
		t.Fatal("accepted duplicate local listener")
	}
}

func TestParseWizardServerAddresses(t *testing.T) {
	for _, input := range []string{"developer@dev.example.invalid:2222", "developer@[::1]:2200", "[2001:db8::1]", "dev.example.invalid", "2001:db8::2"} {
		if _, err := parseServerAddress(input); err != nil {
			t.Fatalf("%s: %v", input, err)
		}
	}
	for _, input := range []string{"-bad", "dev:0", "user@@dev", "dev;touch", "user name@dev", "ssh://dev"} {
		if _, err := parseServerAddress(input); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
}

func TestNoninteractiveSetupDoesNotReadPrompt(t *testing.T) {
	code, response := run(t, t.TempDir(), "fixture\n", "setup")
	if code != 2 || response.Error == nil {
		t.Fatal(code, response)
	}
}
