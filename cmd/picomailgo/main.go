package main

import (
	"crypto/tls"
	"flag"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	imapserver "github.com/emersion/go-imap/server"

	"picomailgo/embedded"
	"picomailgo/internal/auth"
	"picomailgo/internal/config"
	"picomailgo/internal/db"
	picoimap "picomailgo/internal/imap"
	"picomailgo/internal/logging"
	picosmtp "picomailgo/internal/smtp"
	picotls "picomailgo/internal/tls"
	"picomailgo/internal/web"
)

func main() {
	dkimSelector := flag.String("dkim-selector", "default", "DKIM selector name")
	flag.Parse()

	logging.Setup()

	cfg := config.Load()

	slog.Info("picomailgo starting", "domain", cfg.Server.Domain, "data_dir", cfg.Server.DataDir)

	database, err := db.Open(cfg.DBPath())
	if err != nil {
		slog.Error("open database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	slog.Info("database ready", "path", cfg.DBPath())

	// DKIM: ensure key exists and log DNS record
	{
		signer := picosmtp.NewDKIMSigner(database)
		selector, dnsRecord, err := signer.EnsureKey(cfg.Server.Domain, *dkimSelector)
		if err != nil {
			slog.Error("dkim setup", "error", err)
			os.Exit(1)
		}
		slog.Info("dkim ready",
			"name", selector+"._domainkey."+cfg.Server.Domain,
			"record", dnsRecord,
		)
	}

	// TLS
	tlsCfg, err := picotls.Setup(cfg)
	if err != nil {
		slog.Error("tls setup", "error", err)
		os.Exit(1)
	}
	if tlsCfg != nil {
		slog.Info("TLS enabled", "mode", cfg.TLS.Mode)
	}

	authSvc := auth.NewService(database.Writer, database.Reader, cfg.Auth.JWTSecret)
	relay := picosmtp.NewRelay(cfg, database)

	// Outbound queue processor
	queueStop := make(chan struct{})
	go relay.StartQueueProcessor(queueStop)

	// Web server
	tmplFS, _ := fs.Sub(embedded.TemplateFS, "templates")
	staticFS, _ := fs.Sub(embedded.StaticFS, "static")

	webSrv, err := web.NewServer(cfg, database, authSvc, relay, tmplFS, staticFS)
	if err != nil {
		slog.Error("create web server", "error", err)
		os.Exit(1)
	}

	webLn, err := net.Listen("tcp", cfg.Web.Listen)
	if err != nil {
		slog.Error("listen web", "addr", cfg.Web.Listen, "error", err)
		os.Exit(1)
	}
	if tlsCfg != nil {
		webLn = tls.NewListener(webLn, tlsCfg)
	}
	slog.Info("web server listening", "addr", webLn.Addr())

	go func() {
		if err := http.Serve(webLn, webSrv.Handler()); err != nil {
			slog.Error("web server", "error", err)
			os.Exit(1)
		}
	}()

	// SMTP inbound server (port 25)
	smtpInbound := picosmtp.NewInboundServer(cfg, database)
	if tlsCfg != nil {
		smtpInbound.TLSConfig = tlsCfg
	}
	go func() {
		slog.Info("smtp inbound listening", "addr", cfg.SMTP.InboundListen)
		if err := smtpInbound.ListenAndServe(); err != nil {
			slog.Error("smtp inbound", "error", err)
			os.Exit(1)
		}
	}()

	// SMTP submission server (port 587)
	smtpSubmission := picosmtp.NewSubmissionServer(cfg, database, authSvc, relay)
	if tlsCfg != nil {
		smtpSubmission.TLSConfig = tlsCfg
	}
	go func() {
		slog.Info("smtp submission listening", "addr", cfg.SMTP.SubmissionListen)
		if err := smtpSubmission.ListenAndServe(); err != nil {
			slog.Error("smtp submission", "error", err)
			os.Exit(1)
		}
	}()

	// IMAP server
	imapBackend := picoimap.NewBackend(database, authSvc)
	imapSrv := imapserver.New(imapBackend)
	imapSrv.Addr = cfg.IMAP.Listen
	imapSrv.AllowInsecureAuth = true
	if tlsCfg != nil {
		imapSrv.TLSConfig = tlsCfg
	}
	go func() {
		slog.Info("imap listening", "addr", cfg.IMAP.Listen)
		if err := imapSrv.ListenAndServe(); err != nil {
			slog.Error("imap", "error", err)
			os.Exit(1)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	slog.Info("shutting down")
	close(queueStop)
	smtpInbound.Close()
	smtpSubmission.Close()
	imapSrv.Close()
}
