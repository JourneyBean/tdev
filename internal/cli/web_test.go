package cli

import (
	"strings"
	"testing"

	"tdev/internal/config"
)

func TestEffectiveHTTPRejectsExpandedHostnameConflicts(t *testing.T) {
	services := []config.HTTPService{
		{Name: "web", Hostname: "demo", TargetHost: "localhost", TargetPort: 5173},
		{Name: "legacy", Hostname: "demo.tdev.localhost", TargetHost: "localhost", TargetPort: 8080},
	}
	if err := config.ValidateHTTP(services); err != nil {
		t.Fatal(err)
	}
	if _, err := effectiveHTTP(services, "tdev.localhost"); err == nil || !strings.Contains(err.Error(), "duplicate HTTP hostname") {
		t.Fatalf("expected a duplicate hostname after expansion, got %v", err)
	}
	if services[0].Hostname != "demo" {
		t.Fatalf("expansion changed the saved declaration: %+v", services)
	}
}
