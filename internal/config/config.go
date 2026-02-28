package config

import (
	"os"
	"path/filepath"
)

type Config struct {
	Server ServerConfig
	Web    WebConfig
	SMTP   SMTPConfig
	IMAP   IMAPConfig
	TLS    TLSConfig
	Auth   AuthConfig
}

type ServerConfig struct {
	Domain  string
	DataDir string
}

type WebConfig struct {
	Listen     string
	HTTPListen string
}

type SMTPConfig struct {
	InboundListen    string
	SubmissionListen string
}

type IMAPConfig struct {
	Listen string
}

type TLSConfig struct {
	Mode     string
	CertFile string
	KeyFile  string
}

type AuthConfig struct {
	JWTSecret string
}

func Load() *Config {
	cfg := defaults()
	applyEnv(cfg)
	return cfg
}

func defaults() *Config {
	return &Config{
		Server: ServerConfig{
			Domain:  "localhost",
			DataDir: "./data",
		},
		Web: WebConfig{
			Listen:     ":8080",
			HTTPListen: ":80",
		},
		SMTP: SMTPConfig{
			InboundListen:    ":2525",
			SubmissionListen: ":5587",
		},
		IMAP: IMAPConfig{
			Listen: ":9993",
		},
		TLS: TLSConfig{
			Mode: "none",
		},
		Auth: AuthConfig{
			JWTSecret: "change-me",
		},
	}
}

func applyEnv(cfg *Config) {
	envStr(&cfg.Server.Domain, "MAIL_DOMAIN")
	envStr(&cfg.Server.DataDir, "DATA_DIR")
	envStr(&cfg.Web.Listen, "WEB_LISTEN")
	envStr(&cfg.Web.HTTPListen, "HTTP_LISTEN")
	envStr(&cfg.SMTP.InboundListen, "SMTP_INBOUND_LISTEN")
	envStr(&cfg.SMTP.SubmissionListen, "SMTP_SUBMISSION_LISTEN")
	envStr(&cfg.IMAP.Listen, "IMAP_LISTEN")
	envStr(&cfg.TLS.Mode, "TLS_MODE")
	envStr(&cfg.TLS.CertFile, "TLS_CERT_FILE")
	envStr(&cfg.TLS.KeyFile, "TLS_KEY_FILE")
	envStr(&cfg.Auth.JWTSecret, "JWT_SECRET")
}

func envStr(target *string, key string) {
	if v := os.Getenv(key); v != "" {
		*target = v
	}
}

// DBPath returns the full path to the SQLite database file.
func (c *Config) DBPath() string {
	return filepath.Join(c.Server.DataDir, "mail.db")
}
