package config

import "strings"

// HTTPService is a project-owned route; tunnel addresses belong to the client.
type HTTPService struct {
	Autostart  *bool  `yaml:"autostart,omitempty" json:"autostart,omitempty"`
	Name       string `yaml:"name" json:"name"`
	Hostname   string `yaml:"hostname" json:"hostname"`
	TargetHost string `yaml:"target_host" json:"target_host"`
	TargetPort int    `yaml:"target_port" json:"target_port"`
}

func ValidHTTPHostname(host string) bool {
	if len(host) > 253 || host != strings.ToLower(host) || !strings.HasSuffix(host, ".localhost") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

func ValidateHTTP(services []HTTPService) error {
	names, hosts := map[string]bool{}, map[string]bool{}
	for _, h := range services {
		if !ValidName(h.Name) || names[h.Name] || !ValidHTTPHostname(h.Hostname) || hosts[h.Hostname] {
			return Invalid("HTTP services require unique names and lowercase *.localhost hostnames")
		}
		f := Forward{Direction: "L", BindAddress: "127.0.0.1", ListenPort: 1, TargetHost: h.TargetHost, TargetPort: h.TargetPort}
		if err := f.Validate(); err != nil {
			return err
		}
		names[h.Name], hosts[h.Hostname] = true, true
	}
	return nil
}

// AutoStart defaults to true for existing declarations and older API responses.
func (h HTTPService) AutoStart() bool { return h.Autostart == nil || *h.Autostart }

// SameRoute compares the running endpoint, independent of startup preferences.
func (h HTTPService) SameRoute(other HTTPService) bool {
	h.Autostart, other.Autostart = nil, nil
	return h == other
}
