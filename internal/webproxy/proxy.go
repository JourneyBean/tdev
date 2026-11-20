// Package webproxy serves project HTTP routes on a loopback-only listener.
package webproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"tdev/internal/config"
	"tdev/internal/forward"
	"tdev/internal/reconnect"
	"tdev/internal/sshx"
)

type State struct {
	TargetHost string `json:"target_host,omitempty"`
	TargetPort int    `json:"target_port,omitempty"`
	Server     string `json:"server"`
	Project    string `json:"project"`
	Name       string `json:"name"`
	Hostname   string `json:"hostname"`
	URL        string `json:"url"`
	HTTPSURL   string `json:"https_url,omitempty"`
	State      string `json:"state"`
	Error      string `json:"error,omitempty"`
}

type route struct {
	service                   config.HTTPService
	certificate               *tls.Certificate
	server, project, identity string
	mu                        sync.Mutex
	state, failure            string
	closed                    bool
	local                     bool
	address                   string
	group                     *forward.Group
	transport                 *http.Transport
	proxy                     *httputil.ReverseProxy
	connections               map[*trackedConn]bool
	done                      chan struct{}
}

type Manager struct {
	mu           sync.RWMutex
	routes       map[string]*route
	httpListener net.Listener
	tlsListener  net.Listener
	server       *http.Server
	port         int
	httpsPort    int
}

func New() *Manager { return &Manager{routes: map[string]*route{}} }

// Start registers a complete project's routes atomically. Existing registrations
// must be stopped before changing their definitions or connection preferences.
func (m *Manager) Start(ctx context.Context, server, project, identity string, port, httpsPort int, services []config.HTTPService, local bool, alias string, opts sshx.Options, policy reconnect.Policy, acquire forward.Acquire) error {
	if err := config.ValidateHTTPRoutes(services); err != nil {
		return err
	}
	if !config.ValidName(server) || !config.ValidName(project) {
		return config.Invalid("invalid web project identity")
	}
	if port < 1 || port > 65535 || httpsPort < 1 || httpsPort > 65535 || port == httpsPort {
		return config.Invalid("HTTP and HTTPS proxy ports must be distinct and between 1 and 65535")
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	if !local {
		if err := opts.Validate(); err != nil {
			return err
		}
		if err := sshx.CheckAlias(alias); err != nil {
			return config.Invalid("invalid web SSH alias")
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.server != nil && (m.port != port || m.httpsPort != httpsPort) {
		return config.Invalid("web proxy already listens on HTTP port %d and HTTPS port %d; stop its routes before changing the ports", m.port, m.httpsPort)
	}
	for _, h := range services {
		if r := m.routes[h.Hostname]; r != nil && (r.server != server || r.project != project || r.identity != identity || !r.service.SameRoute(h)) {
			return config.Invalid("HTTP hostname %s is already registered by %s/%s; stop it or choose another hostname", h.Hostname, r.server, r.project)
		}
	}
	// A manual open may add services omitted by automatic startup. Existing
	// endpoints must remain unchanged.
	for _, r := range m.routes {
		if r.server != server || r.project != project {
			continue
		}
		found := false
		for _, h := range services {
			if h.SameRoute(r.service) {
				found = true
			}
		}
		if !found {
			return config.Invalid("HTTP project routes changed; stop %s/%s before reopening", server, project)
		}
	}
	if len(services) == 0 {
		return nil
	}
	certificates := make(map[string]*tls.Certificate)
	for _, h := range services {
		if m.routes[h.Hostname] != nil {
			continue
		}
		cert, err := routeCertificate(h.Hostname)
		if err != nil {
			return fmt.Errorf("create TLS certificate for %s: %w", h.Hostname, err)
		}
		certificates[h.Hostname] = cert
	}
	if m.server == nil {
		httpListener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			return config.Invalid("listen for HTTP proxy: %v", err)
		}
		httpsListener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(httpsPort)))
		if err != nil {
			_ = httpListener.Close()
			return config.Invalid("listen for HTTPS proxy: %v", err)
		}
		tlsConfig := &tls.Config{GetCertificate: m.certificate, NextProtos: []string{"http/1.1"}}
		m.port, m.httpsPort = port, httpsPort
		m.httpListener, m.tlsListener = httpListener, tls.NewListener(httpsListener, tlsConfig)
		s := &http.Server{Handler: m, TLSConfig: tlsConfig.Clone(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, ErrorLog: log.New(io.Discard, "", 0)}
		m.server = s
		for _, listener := range []net.Listener{m.httpListener, m.tlsListener} {
			go func() { _ = s.Serve(listener) }()
		}
	}
	for _, h := range services {
		if m.routes[h.Hostname] != nil {
			continue
		}
		r := &route{local: local, service: h, certificate: certificates[h.Hostname], server: server, project: project, identity: identity, state: "ready", address: net.JoinHostPort(h.TargetHost, strconv.Itoa(h.TargetPort)), connections: map[*trackedConn]bool{}, done: make(chan struct{})}
		if !local {
			// Reserve an ephemeral address; OpenSSH confirms ownership before this route
			// becomes ready. A competing bind is reported as a failed route.
			l, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				r.state, r.failure = "failed", "allocate HTTP tunnel: "+err.Error()
				close(r.done)
			} else {
				p := l.Addr().(*net.TCPAddr).Port
				_ = l.Close()
				r.address, r.state = net.JoinHostPort("127.0.0.1", strconv.Itoa(p)), "connecting"
				f := config.Forward{ID: h.Name, Direction: "L", BindAddress: "127.0.0.1", ListenPort: p, TargetHost: h.TargetHost, TargetPort: h.TargetPort}
				if acquire != nil {
					r.group = forward.StartShared(ctx, opts, []config.Forward{f}, policy, acquire)
				} else {
					r.group = forward.Start(ctx, alias, opts, []config.Forward{f}, policy)
				}
				go r.events()
			}
		} else {
			close(r.done)
		}
		r.transport = &http.Transport{DialContext: r.dial, MaxIdleConnsPerHost: 16, IdleConnTimeout: 60 * time.Second, TLSHandshakeTimeout: 10 * time.Second}
		if h.Scheme() == "https" {
			// Development upstreams may use independent self-signed certificates.
			// Use the configured target for SNI even when dialing a local SSH tunnel.
			r.transport.TLSClientConfig = &tls.Config{ServerName: h.TargetHost, InsecureSkipVerify: true}
		}
		r.proxy = &httputil.ReverseProxy{
			Rewrite: func(p *httputil.ProxyRequest) {
				p.Out.URL.Scheme, p.Out.URL.Host = h.Scheme(), r.address
				p.Out.Host = p.In.Host
				p.SetXForwarded()
			},
			Transport: r.transport, FlushInterval: -1,
			ErrorLog: log.New(io.Discard, "", 0),
			ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
				http.Error(w, "HTTP service unavailable", http.StatusServiceUnavailable)
			},
		}
		m.routes[h.Hostname] = r
	}
	return nil
}

func (r *route) events() {
	defer close(r.done)
	for e := range r.group.Events {
		r.mu.Lock()
		if !r.closed {
			r.state, r.failure = e.State, e.Error
		}
		r.mu.Unlock()
	}
}

func (m *Manager) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	host := req.Host
	if strings.Contains(host, ":") {
		var err error
		var port string
		host, port, err = net.SplitHostPort(host)
		n, portErr := strconv.Atoi(port)
		if err != nil || portErr != nil || n < 1 || n > 65535 || strings.ContainsAny(req.Host, "[]") {
			http.Error(w, "invalid Host", http.StatusBadRequest)
			return
		}
	}
	host = strings.ToLower(host)
	if req.URL.IsAbs() || req.Method == "CONNECT" {
		http.Error(w, "invalid Host or request target", http.StatusBadRequest)
		return
	}
	m.mu.RLock()
	r := m.routes[host]
	m.mu.RUnlock()
	if r == nil {
		if !config.ValidHTTPHostname(host) {
			http.Error(w, "invalid Host", http.StatusBadRequest)
			return
		}
		http.NotFound(w, req)
		return
	}
	r.mu.Lock()
	ready := r.state == "ready"
	r.mu.Unlock()
	if !ready {
		http.Error(w, "HTTP tunnel unavailable", http.StatusServiceUnavailable)
		return
	}
	r.proxy.ServeHTTP(w, req)
}

func (m *Manager) States() []State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	states := []State{}
	for _, r := range m.routes {
		r.mu.Lock()
		httpHost := net.JoinHostPort(r.service.Hostname, strconv.Itoa(m.port))
		httpsHost := net.JoinHostPort(r.service.Hostname, strconv.Itoa(m.httpsPort))
		states = append(states, State{TargetHost: r.service.TargetHost, TargetPort: r.service.TargetPort, Server: r.server, Project: r.project, Name: r.service.Name, Hostname: r.service.Hostname, URL: "http://" + httpHost, HTTPSURL: "https://" + httpsHost, State: r.state, Error: r.failure})
		r.mu.Unlock()
	}
	sort.Slice(states, func(i, j int) bool { return states[i].Hostname < states[j].Hostname })
	return states
}

func (m *Manager) Stop(server, project string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for host, r := range m.routes {
		if server != "" && (r.server != server || r.project != project) {
			continue
		}
		delete(m.routes, host)
		r.mu.Lock()
		r.closed, r.state = true, "stopped"
		connections := make([]*trackedConn, 0, len(r.connections))
		for c := range r.connections {
			connections = append(connections, c)
		}
		r.mu.Unlock()
		for _, c := range connections {
			_ = c.Close()
		}
		r.transport.CloseIdleConnections()
		if r.group != nil {
			r.group.Close()
			<-r.done
		}
	}
	if len(m.routes) == 0 && m.server != nil {
		_ = m.server.Close()
		_ = m.httpListener.Close()
		_ = m.tlsListener.Close()
		m.server, m.httpListener, m.tlsListener = nil, nil, nil
	}
}

type trackedConn struct {
	net.Conn
	route *route
	once  sync.Once
}

func (c *trackedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.route.mu.Lock(); delete(c.route.connections, c); c.route.mu.Unlock() })
	return err
}
func (r *route) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	r.mu.Lock()
	ready := r.state == "ready"
	r.mu.Unlock()
	if !ready {
		return nil, errors.New("HTTP tunnel unavailable")
	}
	c, err := (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, "tcp", r.address)
	if err != nil {
		return nil, err
	}
	tracked := &trackedConn{Conn: c, route: r}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != "ready" {
		_ = c.Close()
		return nil, errors.New("HTTP tunnel unavailable")
	}
	r.connections[tracked] = true
	return tracked, nil
}

// Tunnel describes a route's transport without collecting HTTP requests.
type Tunnel struct {
	Hostname, Socket, State string
	Server, Project, Name   string
	Local                   bool
}

func (m *Manager) Tunnels() []Tunnel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []Tunnel
	for _, r := range m.routes {
		r.mu.Lock()
		state := r.state
		r.mu.Unlock()
		row := Tunnel{Server: r.server, Project: r.project, Name: r.service.Name, Hostname: r.service.Hostname, State: state, Local: r.local}
		if r.group != nil {
			for _, socket := range r.group.Sockets() {
				row.Socket = socket
			}
		}
		result = append(result, row)
	}
	return result
}
