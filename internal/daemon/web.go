package daemon

import (
	"fmt"
	"tdev/internal/config"
	"tdev/internal/protocol"
)

func webKey(req Request) string { return "web/" + req.Server + "/" + req.Name }

func (s *supervisor) startWeb(req Request, restoring bool) (State, error) {
	s.webMu.Lock()
	defer s.webMu.Unlock()
	if !config.ValidName(req.Server) || !config.ValidName(req.Name) {
		return State{}, config.Invalid("invalid web project identity")
	}
	if !req.Local {
		if err := sshRequest(req); err != nil {
			return State{}, err
		}
	}
	if err := req.Policy.Validate(); err != nil {
		return State{}, err
	}
	if err := config.ValidateHTTPRoutes(req.HTTP); err != nil {
		return State{}, err
	}
	s.mu.Lock()
	if s.ctx.Err() != nil || s.stopping {
		s.mu.Unlock()
		return State{}, protocol.Fail("DAEMON_UNAVAILABLE", 6, "daemon is stopping")
	}
	if req.Automatic && (s.webStopped[webKey(req)] || s.desired[webKey(req)].Op == "web") {
		s.mu.Unlock()
		return State{Protocol: Protocol}, nil
	}
	if restoring {
		if _, ok := s.desired[webKey(req)]; !ok {
			s.mu.Unlock()
			return State{Protocol: Protocol}, nil
		}
	}
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()
	if req.WebHTTPSPort == 0 {
		req.WebHTTPSPort = config.DefaultWebHTTPSPort(req.WebPort)
	}
	opts := req.SSH
	opts.MasterDirectory = s.client.Dir
	if err := s.web.Start(s.ctx, req.Server, req.Name, identity(req)+"/"+req.ProjectID+fmt.Sprintf("/%d/%s", req.Policy.Attempts, req.Policy.Interval), req.WebPort, req.WebHTTPSPort, req.HTTP, req.Local, req.Alias, opts, req.Policy, s.sharedTransport(req)); err != nil {
		return State{}, err
	}
	s.mu.Lock()
	if s.ctx.Err() != nil || s.stopping {
		s.mu.Unlock()
		s.web.Stop(req.Server, req.Name)
		return State{}, protocol.Fail("DAEMON_UNAVAILABLE", 6, "daemon is stopping")
	}
	if req.Automatic {
		s.mu.Unlock()
		return s.status(), nil
	}
	delete(s.webStopped, webKey(req))
	key := webKey(req)
	previous, existed := s.desired[key]
	req.Op, req.Owner = "web", ""
	s.desired[key] = req
	err := s.save()
	if err != nil {
		if existed {
			s.desired[key] = previous
		} else {
			delete(s.desired, key)
		}
	}
	s.mu.Unlock()
	if err != nil {
		if !existed {
			s.web.Stop(req.Server, req.Name)
		}
		return State{}, err
	}
	return s.status(), nil
}

func (s *supervisor) stopWeb(req Request) (State, error) {
	if !config.ValidName(req.Server) || !config.ValidName(req.Name) {
		return State{}, config.Invalid("specify web server/project")
	}
	s.webMu.Lock()
	defer s.webMu.Unlock()
	s.mu.Lock()
	key := webKey(req)
	previous, existed := s.desired[key]
	delete(s.desired, key)
	err := s.save()
	if err != nil && existed {
		s.desired[key] = previous
	}
	s.mu.Unlock()
	if err != nil {
		return State{}, err
	}
	s.mu.Lock()
	s.webStopped[key] = true
	s.mu.Unlock()
	s.web.Stop(req.Server, req.Name)
	return s.status(), nil
}
