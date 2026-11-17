package daemon

import (
	"fmt"
	"sort"

	"tdev/internal/reconnect"
)

// WebSource names a project host and its independent discovery retry policy.
// The CLI service callback reads project definitions on that host.
type WebSource struct {
	Server string
	Policy reconnect.Policy
}

func (s *supervisor) startWebAutostart() {
	if s.webDiscovery == nil {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		result, err := s.webDiscovery(s.ctx, Request{Op: "web-autostart-sources"})
		if err != nil {
			s.webAutostartError("configuration", err)
			return
		}
		sources, ok := result.([]WebSource)
		if !ok {
			s.webAutostartError("configuration", fmt.Errorf("invalid discovery sources response"))
			return
		}
		for _, source := range sources {
			if s.ctx.Err() != nil {
				return
			}
			s.wg.Add(1)
			go func(source WebSource) { defer s.wg.Done(); s.discoverWeb(source) }(source)
		}
	}()
}

func (s *supervisor) discoverWeb(source WebSource) {
	if err := source.Policy.Validate(); err != nil {
		s.webAutostartError(source.Server, err)
		return
	}
	budget := reconnect.Budget{Policy: source.Policy}
	for s.ctx.Err() == nil {
		result, err := s.webDiscovery(s.ctx, Request{Op: "web-autostart-projects", Server: source.Server})
		if err == nil {
			requests, ok := result.([]Request)
			if !ok {
				s.webAutostartError(source.Server, fmt.Errorf("invalid project discovery response"))
				return
			}
			s.webAutostartError(source.Server, nil)
			for _, req := range requests {
				if s.ctx.Err() != nil {
					return
				}
				req.Automatic = true
				// Only the discovery source itself can register its server's routes.
				req.Server = source.Server
				if _, err = s.startWeb(req, false); err != nil {
					s.webAutostartError(source.Server+"/"+req.Name, err)
				}
			}
			return
		}
		if s.ctx.Err() != nil {
			return
		}
		s.webAutostartError(source.Server, err)
		if !reconnect.Retryable(err) {
			return
		}
		if err = budget.Next(s.ctx); err != nil {
			if s.ctx.Err() == nil {
				s.webAutostartError(source.Server, err)
			}
			return
		}
	}
}

func (s *supervisor) webAutostartError(source string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		delete(s.webAutostartErrors, source)
	} else {
		s.webAutostartErrors[source] = err.Error()
	}
}

// Called with s.mu held. Errors disappear after a source successfully recovers.
func (s *supervisor) webAutostartWarnings() []string {
	warnings := []string{}
	for source, err := range s.webAutostartErrors {
		warnings = append(warnings, "web autostart "+source+": "+err)
	}
	sort.Strings(warnings)
	return warnings
}
