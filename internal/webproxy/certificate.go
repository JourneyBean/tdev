package webproxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"strings"
	"time"
)

func routeCertificate(host string) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	template := &x509.Certificate{
		Subject:     pkix.Name{CommonName: host},
		DNSNames:    []string{host},
		NotBefore:   now.Add(-5 * time.Minute),
		NotAfter:    now.AddDate(1, 0, 0),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	cert, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{Certificate: [][]byte{cert}, PrivateKey: key}, nil
}

func (m *Manager) certificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if r := m.routes[strings.ToLower(hello.ServerName)]; r != nil {
		return r.certificate, nil
	}
	return nil, fmt.Errorf("no web route for TLS server name %q", hello.ServerName)
}
