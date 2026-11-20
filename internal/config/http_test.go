package config

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestHTTPDeclarationRoundTrip(t *testing.T) {
	s := Store{Root: t.TempDir()}
	text := "demo:\n  dir: /tmp\n  http:\n    - name: web\n      hostname: demo.localhost\n      target_host: localhost\n      target_port: 5173\n"
	if err := os.WriteFile(filepath.Join(s.Root, "config.yaml"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := s.Get("demo")
	if err != nil || len(p.HTTP) != 1 {
		t.Fatalf("%+v %v", p, err)
	}
	saved, err := s.Update(context.Background(), "demo", p.Revision, func(current *Project) error { current.Windows[0].Command = "echo ready"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Get("demo")
	if err != nil || !reflect.DeepEqual(p.HTTP, again.HTTP) || saved.Revision != p.Revision+1 {
		t.Fatalf("%+v %v", again, err)
	}
}
func TestHTTPValidation(t *testing.T) {
	h := HTTPService{Name: "web", Hostname: "demo.localhost", TargetHost: "localhost", TargetPort: 5173}
	if err := ValidateHTTP([]HTTPService{h}); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"localhost", "demo.example.invalid", "Demo.localhost", "*.localhost", "a..localhost", "-a.localhost", "demo.localhost:80", "demo_localhost", "a-.localhost"} {
		bad := h
		bad.Hostname = host
		if err := ValidateHTTP([]HTTPService{bad}); err == nil {
			t.Errorf("accepted %q", host)
		}
	}
	if err := ValidateHTTP([]HTTPService{h, h}); err == nil {
		t.Fatal("accepted duplicates")
	}
	explicitHTTP := h
	explicitHTTP.TargetScheme = "http"
	if !h.SameRoute(explicitHTTP) {
		t.Fatal("explicit HTTP did not match the default route")
	}
	https := h
	https.TargetScheme = "https"
	if err := ValidateHTTP([]HTTPService{https}); err != nil || h.SameRoute(https) {
		t.Fatalf("HTTPS should be a valid, distinct route: %v", err)
	}
	https.TargetScheme = "wss"
	if err := ValidateHTTP([]HTTPService{https}); err == nil {
		t.Fatal("accepted wss as target_scheme")
	}
	h.TargetPort = 0
	if err := ValidateHTTP([]HTTPService{h}); err == nil {
		t.Fatal("accepted invalid port")
	}
}

func TestHTTPAutostartDefaultsAndRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		setting string
		want    bool
	}{{"", true}, {"      autostart: true\n", true}, {"      autostart: false\n", false}} {
		t.Run(tc.setting, func(t *testing.T) {
			s := Store{Root: t.TempDir()}
			text := "demo:\n  dir: /tmp\n  http:\n    - name: web\n      hostname: demo.localhost\n      target_host: localhost\n      target_port: 5173\n" + tc.setting
			if err := os.WriteFile(filepath.Join(s.Root, "config.yaml"), []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			p, err := s.Get("demo")
			if err != nil {
				t.Fatal(err)
			}
			if got := p.HTTP[0].AutoStart(); got != tc.want {
				t.Fatalf("autostart=%v want %v", got, tc.want)
			}
			_, err = s.Update(context.Background(), "demo", p.Revision, func(p *Project) error { p.Windows[0].Command = "echo ready"; return nil })
			if err != nil {
				t.Fatal(err)
			}
			p, err = s.Get("demo")
			if err != nil || p.HTTP[0].AutoStart() != tc.want {
				t.Fatalf("saved HTTP: %+v %v", p.HTTP, err)
			}
		})
	}
	s := Store{Root: t.TempDir()}
	if err := os.WriteFile(filepath.Join(s.Root, "config.yaml"), []byte("demo:\n  dir: /tmp\n  http:\n    - name: web\n      hostname: demo.localhost\n      target_host: localhost\n      target_port: 5173\n      autostart: always\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("demo"); err == nil {
		t.Fatal("accepted invalid autostart policy")
	}
}
