package config

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Server ServerConfig `toml:"server"`
	Web    WebConfig    `toml:"web"`
	SMTP   SMTPConfig   `toml:"smtp"`
	IMAP   IMAPConfig   `toml:"imap"`
	TLS    TLSConfig    `toml:"tls"`
	Auth   AuthConfig   `toml:"auth"`
}

type ServerConfig struct {
	Hostname string `toml:"hostname"`
	DataDir  string `toml:"data_dir"`
}

type WebConfig struct {
	Listen     string `toml:"listen"`
	HTTPListen string `toml:"http_listen"`
}

type SMTPConfig struct {
	InboundListen    string `toml:"inbound_listen"`
	SubmissionListen string `toml:"submission_listen"`
}

type IMAPConfig struct {
	Listen string `toml:"listen"`
}

type TLSConfig struct {
	Mode     string `toml:"mode"`
	CertFile string `toml:"cert_file"`
	KeyFile  string `toml:"key_file"`
}

type AuthConfig struct {
	JWTSecret string `toml:"jwt_secret"`
}

func Load(path string) (*Config, error) {
	cfg := defaults()

	if path != "" {
		if _, err := toml.DecodeFile(path, cfg); err != nil {
			return nil, err
		}
	}

	applyEnv(cfg)
	return cfg, nil
}

func defaults() *Config {
	return &Config{
		Server: ServerConfig{
			Hostname: "localhost",
			DataDir:  "./data",
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
	envStr(&cfg.Server.Hostname, "GOGOMAIL_HOSTNAME")
	envStr(&cfg.Server.DataDir, "GOGOMAIL_DATA_DIR")
	envStr(&cfg.Web.Listen, "GOGOMAIL_WEB_LISTEN")
	envStr(&cfg.Web.HTTPListen, "GOGOMAIL_HTTP_LISTEN")
	envStr(&cfg.SMTP.InboundListen, "GOGOMAIL_SMTP_INBOUND_LISTEN")
	envStr(&cfg.SMTP.SubmissionListen, "GOGOMAIL_SMTP_SUBMISSION_LISTEN")
	envStr(&cfg.IMAP.Listen, "GOGOMAIL_IMAP_LISTEN")
	envStr(&cfg.TLS.Mode, "GOGOMAIL_TLS_MODE")
	envStr(&cfg.TLS.CertFile, "GOGOMAIL_TLS_CERT_FILE")
	envStr(&cfg.TLS.KeyFile, "GOGOMAIL_TLS_KEY_FILE")
	envStr(&cfg.Auth.JWTSecret, "GOGOMAIL_JWT_SECRET")
}

func envStr(target *string, key string) {
	if v := os.Getenv(key); v != "" {
		*target = v
	}
}

// DBPath returns the full path to the SQLite database file.
func (c *Config) DBPath() string {
	return filepath.Join(c.Server.DataDir, "gogomail.db")
}
