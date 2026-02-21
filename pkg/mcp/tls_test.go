// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generateTestCert creates a self-signed certificate and key PEM files
// in the given directory. Returns the cert and key file paths.
func generateTestCert(t *testing.T, dir string) (certFile, keyFile string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)

	certFile = filepath.Join(dir, "tls.crt")
	keyFile = filepath.Join(dir, "tls.key")

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	require.NoError(t, os.WriteFile(certFile, certPEM, 0600))

	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	require.NoError(t, os.WriteFile(keyFile, keyPEM, 0600))

	return certFile, keyFile
}

func TestTLSConfig_Enabled(t *testing.T) {
	tests := []struct {
		name     string
		cfg      TLSConfig
		expected bool
	}{
		{"empty", TLSConfig{}, false},
		{"cert only", TLSConfig{CertFile: "/path/cert"}, true},
		{"both", TLSConfig{CertFile: "/path/cert", KeyFile: "/path/key"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.cfg.Enabled())
		})
	}
}

func TestTLSConfig_Validate(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateTestCert(t, dir)

	tests := []struct {
		name    string
		cfg     TLSConfig
		wantErr string
	}{
		{"disabled", TLSConfig{}, ""},
		{"valid", TLSConfig{CertFile: certFile, KeyFile: keyFile}, ""},
		{"cert without key", TLSConfig{CertFile: certFile}, "--tls-key-file is required"},
		{"key without cert", TLSConfig{KeyFile: keyFile}, "--tls-cert-file is required"},
		{"cert not found", TLSConfig{CertFile: "/nonexistent/cert", KeyFile: keyFile}, "TLS cert file"},
		{"key not found", TLSConfig{CertFile: certFile, KeyFile: "/nonexistent/key"}, "TLS key file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

func TestCertReloader_InitialLoad(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateTestCert(t, dir)

	reloader, err := newCertReloader(certFile, keyFile)
	require.NoError(t, err)

	cert, err := reloader.GetCertificate(nil)
	require.NoError(t, err)
	assert.NotNil(t, cert)
}

func TestCertReloader_InvalidCert(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "bad.crt")
	keyFile := filepath.Join(dir, "bad.key")
	require.NoError(t, os.WriteFile(certFile, []byte("not a cert"), 0600))
	require.NoError(t, os.WriteFile(keyFile, []byte("not a key"), 0600))

	_, err := newCertReloader(certFile, keyFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "initial certificate load")
}

func TestCertReloader_ReloadOnMtimeChange(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateTestCert(t, dir)

	reloader, err := newCertReloader(certFile, keyFile)
	require.NoError(t, err)

	cert1, err := reloader.GetCertificate(nil)
	require.NoError(t, err)

	// Force lastCheck to be in the past to trigger reload check
	reloader.mu.Lock()
	reloader.lastCheck = time.Now().Add(-10 * time.Minute)
	reloader.mu.Unlock()

	// Regenerate the certificate (new serial number, different cert)
	generateTestCert(t, dir)

	// This should trigger reload because lastCheck is old
	cert2, err := reloader.GetCertificate(nil)
	require.NoError(t, err)
	assert.NotNil(t, cert2)

	// The certificate should have been reloaded (different raw bytes)
	assert.NotEqual(t, cert1.Certificate[0], cert2.Certificate[0])
}

func TestCertReloader_NoReloadWhenRecent(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateTestCert(t, dir)

	reloader, err := newCertReloader(certFile, keyFile)
	require.NoError(t, err)

	cert1, err := reloader.GetCertificate(nil)
	require.NoError(t, err)

	// Regenerate cert but don't expire the lastCheck
	generateTestCert(t, dir)

	// Should NOT reload because lastCheck is recent
	cert2, err := reloader.GetCertificate(nil)
	require.NoError(t, err)

	// Same certificate (not reloaded)
	assert.Equal(t, cert1.Certificate[0], cert2.Certificate[0])
}

func TestHTTPServer_TLS_ServesHTTPS(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateTestCert(t, dir)

	mcpServer := server.NewMCPServer("test", "1.0.0")
	httpServer := NewHTTPServer(mcpServer, "127.0.0.1:0", "1.0.0")
	httpServer.SetTLSConfig(&TLSConfig{
		CertFile: certFile,
		KeyFile:  keyFile,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.ListenAndServe(ctx)
	}()

	select {
	case <-httpServer.Ready():
	case <-time.After(5 * time.Second):
		t.Fatal("TLS server did not start in time")
	}

	// Shutdown cleanly
	cancel()
	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("TLS server did not shutdown in time")
	}
}

func TestHTTPServer_TLS_HealthzEndpoint(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateTestCert(t, dir)

	// Use a fixed port to allow making HTTP requests
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close() // Release so the server can bind

	mcpServer := server.NewMCPServer("test", "1.0.0")
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	httpServer := NewHTTPServer(mcpServer, addr, "1.0.0")
	httpServer.SetTLSConfig(&TLSConfig{
		CertFile: certFile,
		KeyFile:  keyFile,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.ListenAndServe(ctx)
	}()

	select {
	case <-httpServer.Ready():
	case <-time.After(5 * time.Second):
		t.Fatal("server did not start in time")
	}

	// Make an HTTPS request to healthz
	tlsClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, //nolint:gosec // test only
			},
		},
		Timeout: 5 * time.Second,
	}

	resp, err := tlsClient.Get(fmt.Sprintf("https://%s/healthz", addr))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, resp.TLS != nil, "response should be over TLS")

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "healthy", body["status"])

	cancel()
	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shutdown")
	}
}

func TestHTTPServer_NoTLS_Backward_Compatible(t *testing.T) {
	mcpServer := server.NewMCPServer("test", "1.0.0")
	httpServer := NewHTTPServer(mcpServer, "127.0.0.1:0", "1.0.0")
	// No TLS config set - should work as plain HTTP

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.ListenAndServe(ctx)
	}()

	select {
	case <-httpServer.Ready():
	case <-time.After(5 * time.Second):
		t.Fatal("server did not start in time")
	}

	// Verify the tlsConfig field on our struct is nil (no TLS configured)
	assert.Nil(t, httpServer.tlsConfig)

	cancel()
	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down")
	}
}

func TestHTTPServer_TLS_InvalidCert(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "bad.crt")
	keyFile := filepath.Join(dir, "bad.key")
	require.NoError(t, os.WriteFile(certFile, []byte("not a cert"), 0600))
	require.NoError(t, os.WriteFile(keyFile, []byte("not a key"), 0600))

	mcpServer := server.NewMCPServer("test", "1.0.0")
	httpServer := NewHTTPServer(mcpServer, "127.0.0.1:0", "1.0.0")
	httpServer.SetTLSConfig(&TLSConfig{
		CertFile: certFile,
		KeyFile:  keyFile,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := httpServer.ListenAndServe(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TLS setup")
}
