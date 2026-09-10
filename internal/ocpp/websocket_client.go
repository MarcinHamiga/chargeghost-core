package ocpp

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"

	"github.com/lorenzodonini/ocpp-go/ws"

	"github.com/chargeghost/engine/internal/config"
)

func NewWebSocketClient(cfg *config.Config) (*ws.Client, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}

	parsedURL, err := url.Parse(cfg.ConnectionURL)
	if err != nil {
		return nil, fmt.Errorf("parse connection url: %w", err)
	}

	if !validSecurityProfile(cfg.SecurityProfile) {
		return nil, fmt.Errorf("invalid security profile: %d", cfg.SecurityProfile)
	}

	if cfg.SecurityProfile == 2 && parsedURL.Scheme != "wss" {
		return nil, fmt.Errorf("security profile 2 requires wss:// connection url")
	}

	client, err := newWebSocketClientForScheme(parsedURL.Scheme, cfg)
	if err != nil {
		return nil, err
	}

	applyWebSocketBasicAuth(client, cfg)
	return client, nil
}

func validSecurityProfile(profile int) bool {
	return profile >= 0 && profile <= 2
}

func newWebSocketClientForScheme(scheme string, cfg *config.Config) (*ws.Client, error) {
	if scheme == "wss" {
		tlsConfig, err := newWebSocketTLSConfig(cfg)
		if err != nil {
			return nil, err
		}
		return ws.NewTLSClient(tlsConfig), nil
	}

	if scheme != "ws" {
		return nil, fmt.Errorf("unsupported websocket scheme: %s", scheme)
	}

	return ws.NewClient(), nil
}

func newWebSocketTLSConfig(cfg *config.Config) (*tls.Config, error) {
	// OCPP security profiles require verified TLS 1.2+.
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	if cfg.TLSCAPath != "" {
		caPEM, err := os.ReadFile(cfg.TLSCAPath)
		if err != nil {
			return nil, fmt.Errorf("read tls ca file: %w", err)
		}

		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("invalid tls ca bundle: %s", cfg.TLSCAPath)
		}
		tlsConfig.RootCAs = pool
	}

	if cfg.TLSClientCertPath != "" || cfg.TLSClientKeyPath != "" {
		if cfg.TLSClientCertPath == "" || cfg.TLSClientKeyPath == "" {
			return nil, fmt.Errorf("tls client cert and key must both be set")
		}

		cert, err := tls.LoadX509KeyPair(cfg.TLSClientCertPath, cfg.TLSClientKeyPath)
		if err != nil {
			return nil, fmt.Errorf("load tls client cert/key: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	if cfg.SkipTLSVerify {
		// Certificate-verification bypass is a local-development escape
		// hatch only: refuse it for non-loopback CSMS hosts so a copied
		// dev config cannot silently downgrade production TLS.
		host := ""
		if u, err := url.Parse(cfg.ConnectionURL); err == nil {
			host = u.Hostname()
		}
		if !isLoopbackHost(host) {
			return nil, fmt.Errorf("skip_tls_verify is allowed for loopback CSMS hosts only (got %q)", host)
		}
		slog.Warn("skipping TLS certificate verification for OCPP websocket (loopback dev only)")
		tlsConfig.InsecureSkipVerify = true
	}

	return tlsConfig, nil
}

// isLoopbackHost reports whether host is a loopback address or localhost.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func applyWebSocketBasicAuth(client *ws.Client, cfg *config.Config) {
	// An empty configured password counts as unset: falling through to the
	// keyring/env lookup beats sending "Authorization: Basic" with a blank
	// password the CSMS will reject.
	if cfg.OCPPPassword != nil && *cfg.OCPPPassword != "" {
		client.SetBasicAuth(cfg.OCPPID, *cfg.OCPPPassword)
		return
	}

	if password := config.GetPassword(cfg.OCPPID); password != "" {
		// Prefer the system keyring: falling back to the
		// CHARGEGHOST_PASSWORD environment variable widens exposure
		// (process environment leaks to child processes and inspectors).
		// Warn only when the fallback actually supplied the password, not
		// whenever the variable merely exists alongside a keyring entry.
		if config.PasswordFromEnvFallback(cfg.OCPPID) {
			slog.Warn("using OCPP password from CHARGEGHOST_PASSWORD environment fallback; prefer the system keyring")
		}
		client.SetBasicAuth(cfg.OCPPID, password)
	}
}
