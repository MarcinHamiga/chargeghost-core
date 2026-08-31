package ocpp

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/chargeghost/engine/internal/config"
)

func TestNewWebSocketClient_RejectsProfile2NonWSS(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.SecurityProfile = 2
	cfg.ConnectionURL = "ws://127.0.0.1:8080/CP_1"

	client, err := NewWebSocketClient(cfg)
	require.Nil(t, client)
	require.Error(t, err)
}

func TestNewWebSocketClient_RejectsInvalidSecurityProfile(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.SecurityProfile = 22
	cfg.ConnectionURL = "ws://127.0.0.1:8080/CP_1"

	client, err := NewWebSocketClient(cfg)
	require.Nil(t, client)
	require.Error(t, err)
}

func TestNewWebSocketClient_LoadsCABundle(t *testing.T) {
	caCert, caKey, err := newTestCA()
	require.NoError(t, err)

	serverCert, serverKey, err := newSignedCert(caCert, caKey, false)
	require.NoError(t, err)

	clientCert, clientKey, err := newSignedCert(caCert, caKey, true)
	require.NoError(t, err)

	caPath := writePEMFile(t, "ca.pem", caCert)
	clientCertPath := writePEMFile(t, "client.crt", clientCert)
	clientKeyPath := writePEMFile(t, "client.key", clientKey)

	server := newTLSTestServer(t, serverCert, serverKey, caCert, true)
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.SecurityProfile = 1
	cfg.ConnectionURL = wssURLForServer(server, "/CP_1")
	cfg.TLSCAPath = caPath
	cfg.TLSClientCertPath = clientCertPath
	cfg.TLSClientKeyPath = clientKeyPath

	client, err := NewWebSocketClient(cfg)
	require.NoError(t, err)
	require.NoError(t, client.Start(cfg.ConnectionURL))
	client.Stop()
}

func TestNewWebSocketClient_InvalidCABundleFails(t *testing.T) {
	dir := t.TempDir()
	invalidCAPath := filepath.Join(dir, "ca.pem")
	require.NoError(t, os.WriteFile(invalidCAPath, []byte("not pem"), 0o600))

	cfg := config.DefaultConfig()
	cfg.ConnectionURL = "wss://127.0.0.1:8080/CP_1"
	cfg.TLSCAPath = invalidCAPath

	client, err := NewWebSocketClient(cfg)
	require.Nil(t, client)
	require.Error(t, err)
}

func TestNewWebSocketClient_MissingCABundleFails(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ConnectionURL = "wss://127.0.0.1:8080/CP_1"
	cfg.TLSCAPath = filepath.Join(t.TempDir(), "missing-ca.pem")

	client, err := NewWebSocketClient(cfg)
	require.Nil(t, client)
	require.Error(t, err)
}

func TestNewWebSocketClient_ClientCertWithoutKeyFails(t *testing.T) {
	caCert, caKey, err := newTestCA()
	require.NoError(t, err)
	clientCert, _, err := newSignedCert(caCert, caKey, true)
	require.NoError(t, err)

	caPath := writePEMFile(t, "ca.pem", caCert)
	clientCertPath := writePEMFile(t, "client.crt", clientCert)

	cfg := config.DefaultConfig()
	cfg.ConnectionURL = "wss://127.0.0.1:8080/CP_1"
	cfg.TLSCAPath = caPath
	cfg.TLSClientCertPath = clientCertPath

	client, err := NewWebSocketClient(cfg)
	require.Nil(t, client)
	require.Error(t, err)
}

func TestNewWebSocketClient_ClientKeyWithoutCertFails(t *testing.T) {
	caCert, caKey, err := newTestCA()
	require.NoError(t, err)
	_, clientKey, err := newSignedCert(caCert, caKey, true)
	require.NoError(t, err)

	caPath := writePEMFile(t, "ca.pem", caCert)
	clientKeyPath := writePEMFile(t, "client.key", clientKey)

	cfg := config.DefaultConfig()
	cfg.ConnectionURL = "wss://127.0.0.1:8080/CP_1"
	cfg.TLSCAPath = caPath
	cfg.TLSClientKeyPath = clientKeyPath

	client, err := NewWebSocketClient(cfg)
	require.Nil(t, client)
	require.Error(t, err)
}

func TestNewWebSocketClient_SkipTLSVerify(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()

		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.ConnectionURL = wsURLForHTTPServer(server, "/CP_1")
	cfg.SkipTLSVerify = true

	tlsConfig, err := newWebSocketTLSConfig(cfg)
	require.NoError(t, err)
	require.True(t, tlsConfig.InsecureSkipVerify)

	client, err := NewWebSocketClient(cfg)
	require.NoError(t, err)
	require.NoError(t, client.Start(cfg.ConnectionURL))
	client.Stop()
}

func TestNewWebSocketClient_UsesConfiguredBasicAuth(t *testing.T) {
	server := newBasicAuthTestServer(t, func(r *http.Request) bool {
		user, pass, ok := r.BasicAuth()
		return ok && user == "CP_1" && pass == "profile-password"
	})
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.SecurityProfile = 1
	cfg.ConnectionURL = wsURLForHTTPServer(server, "/CP_1")
	password := "profile-password"
	cfg.OCPPPassword = &password

	client, err := NewWebSocketClient(cfg)
	require.NoError(t, err)
	require.NoError(t, client.Start(cfg.ConnectionURL))
	client.Stop()
}

func TestNewWebSocketClient_UsesPasswordFallbackBasicAuth(t *testing.T) {
	t.Setenv("CHARGEGHOST_PASSWORD", "fallback-password")
	ocppID := t.Name()

	server := newBasicAuthTestServer(t, func(r *http.Request) bool {
		user, pass, ok := r.BasicAuth()
		return ok && user == ocppID && pass == "fallback-password"
	})
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.ConnectionURL = wsURLForHTTPServer(server, "/CP_1")
	cfg.OCPPID = ocppID
	cfg.OCPPPassword = nil

	client, err := NewWebSocketClient(cfg)
	require.NoError(t, err)
	require.NoError(t, client.Start(cfg.ConnectionURL))
	client.Stop()
}

func TestNewWebSocketClient_EmptyConfiguredPasswordFallsBackToKeyring(t *testing.T) {
	keyring.MockInit()
	require.NoError(t, config.SetPassword("CP_EMPTYCFG", "keyring-password"))

	server := newBasicAuthTestServer(t, func(r *http.Request) bool {
		user, pass, ok := r.BasicAuth()
		return ok && user == "CP_EMPTYCFG" && pass == "keyring-password"
	})
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.ConnectionURL = wsURLForHTTPServer(server, "/CP_EMPTYCFG")
	cfg.OCPPID = "CP_EMPTYCFG"
	// A non-nil empty configured password must not win over the keyring:
	// sending blank Basic auth credentials is exactly the 401 failure mode.
	empty := ""
	cfg.OCPPPassword = &empty

	client, err := NewWebSocketClient(cfg)
	require.NoError(t, err)
	require.NoError(t, client.Start(cfg.ConnectionURL))
	client.Stop()
}

func newBasicAuthTestServer(t *testing.T, validAuth func(r *http.Request) bool) *httptest.Server {
	t.Helper()

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !validAuth(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()

		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
}

func newTLSTestServer(
	t *testing.T,
	serverCertPEM []byte,
	serverKeyPEM []byte,
	caPEM []byte,
	requireClientCert bool,
) *httptest.Server {
	t.Helper()

	cert, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
	require.NoError(t, err)

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()

		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	if requireClientCert {
		pool := x509.NewCertPool()
		require.True(t, pool.AppendCertsFromPEM(caPEM))
		server.TLS.ClientAuth = tls.RequireAndVerifyClientCert
		server.TLS.ClientCAs = pool
	}
	server.StartTLS()
	return server
}

func wsURLForHTTPServer(server *httptest.Server, path string) string {
	return "ws" + strings.TrimPrefix(server.URL, "http") + path
}

func wssURLForServer(server *httptest.Server, path string) string {
	return "wss" + strings.TrimPrefix(server.URL, "https") + path
}

func writePEMFile(t *testing.T, name string, pemData []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, pemData, 0o600))
	return path
}

func newTestCA() ([]byte, []byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "ChargeGhost Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(
		&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)},
	)
	return certPEM, keyPEM, nil
}

func newSignedCert(caCertPEM, caKeyPEM []byte, client bool) ([]byte, []byte, error) {
	caCertBlock, _ := pem.Decode(caCertPEM)
	if caCertBlock == nil {
		return nil, nil, fmt.Errorf("decode ca cert")
	}
	caKeyBlock, _ := pem.Decode(caKeyPEM)
	if caKeyBlock == nil {
		return nil, nil, fmt.Errorf("decode ca key")
	}

	caCert, err := x509.ParseCertificate(caCertBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}
	caKey, err := x509.ParsePKCS1PrivateKey(caKeyBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "ChargeGhost Test Cert"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}
	if client {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	} else {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	}

	der, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(
		&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)},
	)
	return certPEM, keyPEM, nil
}
