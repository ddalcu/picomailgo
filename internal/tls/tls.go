package tls

import (
	"crypto/tls"
	"fmt"
	"path/filepath"

	"golang.org/x/crypto/acme/autocert"

	"picomailgo/internal/config"
)

// Setup returns a shared *tls.Config based on the configured mode.
// Returns nil when mode is "none".
func Setup(cfg *config.Config) (*tls.Config, error) {
	switch cfg.TLS.Mode {
	case "none", "":
		return nil, nil

	case "autocert":
		m := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(cfg.Server.Domain),
			Cache:      autocert.DirCache(filepath.Join(cfg.Server.DataDir, "certs")),
		}
		return m.TLSConfig(), nil

	case "manual":
		if cfg.TLS.CertFile == "" || cfg.TLS.KeyFile == "" {
			return nil, fmt.Errorf("tls manual mode requires cert_file and key_file")
		}
		cert, err := tls.LoadX509KeyPair(cfg.TLS.CertFile, cfg.TLS.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load TLS cert: %w", err)
		}
		return &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}, nil

	default:
		return nil, fmt.Errorf("unknown TLS mode: %q (use none, autocert, or manual)", cfg.TLS.Mode)
	}
}
