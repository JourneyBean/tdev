package config

import (
	"net/netip"
	"strings"
)

const (
	DefaultWebHTTPPort  = 24880
	defaultWebHTTPSPort = 24881
)

// DefaultWebHTTPSPort selects an HTTPS port distinct from the configured HTTP port.
func DefaultWebHTTPSPort(httpPort int) int {
	if httpPort == defaultWebHTTPSPort {
		return DefaultWebHTTPPort
	}
	return defaultWebHTTPSPort
}

// HTTPService is a project-owned route; tunnel addresses belong to the client.
type HTTPService struct {
	Autostart    *bool  `yaml:"autostart,omitempty" json:"autostart,omitempty"`
	Name         string `yaml:"name" json:"name"`
	Hostname     string `yaml:"hostname" json:"hostname"`
	TargetScheme string `yaml:"target_scheme,omitempty" json:"target_scheme,omitempty"`
	TargetHost   string `yaml:"target_host" json:"target_host"`
	TargetPort   int    `yaml:"target_port" json:"target_port"`
}

func ValidHTTPHostname(host string) bool {
	return strings.HasSuffix(host, ".localhost") && ValidHTTPDomainSuffix(host)
}

// ValidHTTPDomainSuffix accepts lowercase DNS names without a leading or trailing dot.
func ValidHTTPDomainSuffix(host string) bool {
	if len(host) > 253 {
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
	_, err := netip.ParseAddr(host)
	return err != nil
}

func (h HTTPService) WithDomainSuffix(suffix string) HTTPService {
	if !strings.Contains(h.Hostname, ".") {
		h.Hostname += "." + suffix
	}
	return h
}

func ValidateHTTP(services []HTTPService) error {
	return validateHTTP(services, func(host string) bool {
		return ValidHTTPHostname(host) || !strings.Contains(host, ".") && host != "localhost" && ValidHTTPDomainSuffix(host)
	}, "a lowercase DNS label or *.localhost hostname")
}

func ValidateHTTPRoutes(services []HTTPService) error {
	return validateHTTP(services, func(host string) bool {
		return strings.Contains(host, ".") && ValidHTTPDomainSuffix(host)
	}, "a fully qualified lowercase DNS hostname")
}

func validateHTTP(services []HTTPService, validHost func(string) bool, hostnameFormat string) error {
	names, hosts := map[string]bool{}, map[string]bool{}
	for _, h := range services {
		if !ValidName(h.Name) || names[h.Name] {
			return Invalid("HTTP services require unique valid names: %q", h.Name)
		}
		if !validHost(h.Hostname) {
			return Invalid("HTTP service %s hostname must be %s: %q", h.Name, hostnameFormat, h.Hostname)
		}
		if hosts[h.Hostname] {
			return Invalid("duplicate HTTP hostname %q", h.Hostname)
		}
		if h.Scheme() != "http" && h.Scheme() != "https" {
			return Invalid("HTTP service %s target_scheme must be http or https", h.Name)
		}
		f := Forward{Direction: "L", BindAddress: "127.0.0.1", ListenPort: 1, TargetHost: h.TargetHost, TargetPort: h.TargetPort}
		if err := f.Validate(); err != nil {
			return err
		}
		names[h.Name], hosts[h.Hostname] = true, true
	}
	return nil
}

// Scheme returns the upstream protocol, defaulting to HTTP.
func (h HTTPService) Scheme() string {
	if h.TargetScheme == "" {
		return "http"
	}
	return h.TargetScheme
}

// AutoStart defaults to true for existing declarations and older API responses.
func (h HTTPService) AutoStart() bool { return h.Autostart == nil || *h.Autostart }

// SameRoute compares the running endpoint, independent of startup preferences.
func (h HTTPService) SameRoute(other HTTPService) bool {
	h.Autostart, other.Autostart = nil, nil
	h.TargetScheme, other.TargetScheme = h.Scheme(), other.Scheme()
	return h == other
}
