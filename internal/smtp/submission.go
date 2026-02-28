package smtp

import (
	"io"
	"log/slog"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"gogomail/internal/auth"
	"gogomail/internal/config"
	"gogomail/internal/db"
)

// SubmissionBackend handles authenticated outbound mail submission (port 587).
type SubmissionBackend struct {
	cfg     *config.Config
	db      *db.DB
	authSvc *auth.Service
	relay   *Relay
}

func NewSubmissionBackend(cfg *config.Config, database *db.DB, authSvc *auth.Service, relay *Relay) *SubmissionBackend {
	return &SubmissionBackend{cfg: cfg, db: database, authSvc: authSvc, relay: relay}
}

func (b *SubmissionBackend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	return &SubmissionSession{
		backend: b,
	}, nil
}

// NewSubmissionServer creates an SMTP server for authenticated outbound submission.
func NewSubmissionServer(cfg *config.Config, database *db.DB, authSvc *auth.Service, relay *Relay) *smtp.Server {
	be := NewSubmissionBackend(cfg, database, authSvc, relay)
	s := smtp.NewServer(be)
	s.Addr = cfg.SMTP.SubmissionListen
	s.Domain = cfg.Server.Hostname
	s.ReadTimeout = 60 * time.Second
	s.WriteTimeout = 60 * time.Second
	s.MaxMessageBytes = 25 * 1024 * 1024
	s.MaxRecipients = 50
	s.AllowInsecureAuth = true
	return s
}

// SubmissionSession requires SMTP AUTH before accepting mail.
type SubmissionSession struct {
	backend *SubmissionBackend
	user    *auth.User
	from    string
	to      []string
}

// AuthMechanisms returns the supported SASL mechanisms.
func (s *SubmissionSession) AuthMechanisms() []string {
	return []string{"PLAIN"}
}

// Auth performs SASL authentication.
func (s *SubmissionSession) Auth(mech string) (sasl.Server, error) {
	return sasl.NewPlainServer(func(identity, username, password string) error {
		user, err := s.backend.authSvc.Authenticate(username, password)
		if err != nil {
			return &smtp.SMTPError{
				Code:         535,
				EnhancedCode: smtp.EnhancedCode{5, 7, 8},
				Message:      "Authentication failed",
			}
		}
		s.user = user
		return nil
	}), nil
}

func (s *SubmissionSession) Mail(from string, opts *smtp.MailOptions) error {
	if s.user == nil {
		return &smtp.SMTPError{
			Code:         530,
			EnhancedCode: smtp.EnhancedCode{5, 7, 0},
			Message:      "Authentication required",
		}
	}
	s.from = from
	return nil
}

func (s *SubmissionSession) Rcpt(to string, opts *smtp.RcptOptions) error {
	if s.user == nil {
		return &smtp.SMTPError{
			Code:         530,
			EnhancedCode: smtp.EnhancedCode{5, 7, 0},
			Message:      "Authentication required",
		}
	}
	s.to = append(s.to, to)
	return nil
}

func (s *SubmissionSession) Data(r io.Reader) error {
	if s.user == nil {
		return &smtp.SMTPError{
			Code:         530,
			EnhancedCode: smtp.EnhancedCode{5, 7, 0},
			Message:      "Authentication required",
		}
	}

	raw, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	for _, to := range s.to {
		if err := s.backend.relay.Send(s.from, to, raw); err != nil {
			slog.Warn("submission: relay failed", "to", to, "error", err)
		}
	}

	// Save to sent folder
	s.backend.relay.SaveToSent(s.user.ID, s.from, s.to[0], "", time.Now(), raw)

	slog.Info("submission: message sent", "user", s.user.Username, "to", s.to)
	return nil
}

func (s *SubmissionSession) Reset() {
	s.from = ""
	s.to = nil
}

func (s *SubmissionSession) Logout() error {
	return nil
}
