// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"crypto/tls"
	"fmt"
	"os"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

// TLSConfig holds TLS configuration for the HTTP server.
type TLSConfig struct {
	// CertFile is the path to the TLS certificate PEM file.
	CertFile string
	// KeyFile is the path to the TLS private key PEM file.
	KeyFile string
}

// Enabled returns true if TLS is configured (cert file is set).
func (c *TLSConfig) Enabled() bool {
	return c.CertFile != ""
}

// Validate checks that the TLS configuration is valid.
func (c *TLSConfig) Validate() error {
	if c.CertFile == "" && c.KeyFile == "" {
		return nil // TLS disabled
	}
	if c.CertFile == "" {
		return fmt.Errorf("--tls-cert-file is required when --tls-key-file is set")
	}
	if c.KeyFile == "" {
		return fmt.Errorf("--tls-key-file is required when --tls-cert-file is set")
	}
	// Verify files exist and are readable
	if _, err := os.Stat(c.CertFile); err != nil {
		return fmt.Errorf("TLS cert file: %w", err)
	}
	if _, err := os.Stat(c.KeyFile); err != nil {
		return fmt.Errorf("TLS key file: %w", err)
	}
	return nil
}

const certReloadInterval = 5 * time.Minute

// certReloader loads TLS certificates from disk and reloads them
// when the file modification time changes. It is safe for concurrent use.
type certReloader struct {
	certFile string
	keyFile  string

	mu          sync.RWMutex
	cert        *tls.Certificate
	certModTime time.Time
	keyModTime  time.Time
	lastCheck   time.Time
}

// newCertReloader creates a certReloader and performs the initial certificate load.
func newCertReloader(certFile, keyFile string) (*certReloader, error) {
	r := &certReloader{
		certFile: certFile,
		keyFile:  keyFile,
	}
	if err := r.reload(); err != nil {
		return nil, fmt.Errorf("initial certificate load: %w", err)
	}
	return r, nil
}

// GetCertificate implements the crypto/tls.Config.GetCertificate callback.
// It checks file modification times periodically and reloads if changed.
func (r *certReloader) GetCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.RLock()
	lastCheck := r.lastCheck
	r.mu.RUnlock()

	if time.Since(lastCheck) >= certReloadInterval {
		if err := r.maybeReload(); err != nil {
			klog.ErrorS(err, "failed to reload TLS certificate, using cached")
		}
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cert, nil
}

// maybeReload checks file mtimes and reloads if changed.
func (r *certReloader) maybeReload() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock
	if time.Since(r.lastCheck) < certReloadInterval {
		return nil
	}
	r.lastCheck = time.Now()

	certInfo, err := os.Stat(r.certFile)
	if err != nil {
		return fmt.Errorf("stat cert file: %w", err)
	}
	keyInfo, err := os.Stat(r.keyFile)
	if err != nil {
		return fmt.Errorf("stat key file: %w", err)
	}

	if certInfo.ModTime().Equal(r.certModTime) && keyInfo.ModTime().Equal(r.keyModTime) {
		return nil // No changes
	}

	klog.InfoS("TLS certificate files changed, reloading",
		"certFile", r.certFile, "keyFile", r.keyFile)
	return r.reloadLocked()
}

// reload loads the certificate from disk (acquires write lock).
func (r *certReloader) reload() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reloadLocked()
}

// reloadLocked loads the certificate from disk (caller must hold write lock).
func (r *certReloader) reloadLocked() error {
	cert, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return fmt.Errorf("load certificate: %w", err)
	}

	certInfo, err := os.Stat(r.certFile)
	if err != nil {
		return fmt.Errorf("stat cert file after load: %w", err)
	}
	keyInfo, err := os.Stat(r.keyFile)
	if err != nil {
		return fmt.Errorf("stat key file after load: %w", err)
	}

	r.cert = &cert
	r.certModTime = certInfo.ModTime()
	r.keyModTime = keyInfo.ModTime()
	r.lastCheck = time.Now()

	klog.InfoS("TLS certificate loaded",
		"certFile", r.certFile, "keyFile", r.keyFile)
	return nil
}
