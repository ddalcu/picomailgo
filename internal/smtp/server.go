package smtp

import (
	"database/sql"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/emersion/go-smtp"

	"picomailgo/internal/config"
	"picomailgo/internal/db"
	"picomailgo/internal/email"
)

// InboundBackend handles incoming mail from remote MTAs.
type InboundBackend struct {
	cfg *config.Config
	db  *db.DB
}

func NewInboundBackend(cfg *config.Config, database *db.DB) *InboundBackend {
	return &InboundBackend{cfg: cfg, db: database}
}

func (b *InboundBackend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	return &InboundSession{db: b.db, cfg: b.cfg}, nil
}

// NewInboundServer creates an SMTP server for receiving inbound mail.
func NewInboundServer(cfg *config.Config, database *db.DB) *smtp.Server {
	be := NewInboundBackend(cfg, database)
	s := smtp.NewServer(be)
	s.Addr = cfg.SMTP.InboundListen
	s.Domain = cfg.Server.Domain
	s.ReadTimeout = 60 * time.Second
	s.WriteTimeout = 60 * time.Second
	s.MaxMessageBytes = 25 * 1024 * 1024 // 25MB
	s.MaxRecipients = 50
	s.AllowInsecureAuth = true
	return s
}

// InboundSession represents a single SMTP session for inbound mail.
type InboundSession struct {
	db   *db.DB
	cfg  *config.Config
	from string
	to   []string
}

func (s *InboundSession) Mail(from string, opts *smtp.MailOptions) error {
	s.from = from
	return nil
}

func (s *InboundSession) Rcpt(to string, opts *smtp.RcptOptions) error {
	// Extract local part and verify it's a local user
	parts := strings.SplitN(to, "@", 2)
	if len(parts) != 2 {
		return &smtp.SMTPError{
			Code:         550,
			EnhancedCode: smtp.EnhancedCode{5, 1, 1},
			Message:      "Invalid address",
		}
	}

	username := parts[0]
	var exists int
	err := s.db.Reader.QueryRow("SELECT COUNT(*) FROM users WHERE username = ?", username).Scan(&exists)
	if err != nil || exists == 0 {
		return &smtp.SMTPError{
			Code:         550,
			EnhancedCode: smtp.EnhancedCode{5, 1, 1},
			Message:      "User not found",
		}
	}

	s.to = append(s.to, to)
	return nil
}

func (s *InboundSession) Data(r io.Reader) error {
	raw, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	parsed, err := email.ParseHeaders(raw)
	if err != nil {
		slog.Warn("smtp: failed to parse headers", "error", err)
		// Store anyway with what we have
		parsed = &email.ParsedMessage{
			From:    s.from,
			Subject: "(no subject)",
			Date:    time.Now(),
			Raw:     raw,
		}
	}
	parsed.Raw = raw

	// Override From with envelope sender if parse didn't find it
	if parsed.From == "" {
		parsed.From = s.from
	}

	// Deliver to each recipient's INBOX
	for _, to := range s.to {
		parts := strings.SplitN(to, "@", 2)
		username := parts[0]
		if err := s.deliverToInbox(username, to, parsed); err != nil {
			slog.Error("smtp: failed to deliver", "to", to, "error", err)
			return &smtp.SMTPError{
				Code:         451,
				EnhancedCode: smtp.EnhancedCode{4, 3, 0},
				Message:      "Temporary delivery failure",
			}
		}
	}

	slog.Info("smtp: delivered message", "from", s.from, "to", s.to)
	return nil
}

func (s *InboundSession) deliverToInbox(username, toAddr string, msg *email.ParsedMessage) error {
	tx, err := s.db.Writer.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Get INBOX mailbox ID and next UID
	var mailboxID, uid int64
	err = tx.QueryRow(`
		UPDATE mailboxes SET uid_next = uid_next + 1
		WHERE name = 'INBOX' AND user_id = (SELECT id FROM users WHERE username = ?)
		RETURNING id, uid_next - 1`,
		username,
	).Scan(&mailboxID, &uid)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil // user doesn't exist, silently drop
		}
		return err
	}

	var dateStr *string
	if !msg.Date.IsZero() {
		s := msg.Date.UTC().Format("2006-01-02 15:04:05")
		dateStr = &s
	}

	_, err = tx.Exec(`
		INSERT INTO messages (mailbox_id, uid, from_addr, to_addr, subject, date, size, raw)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		mailboxID, uid, msg.From, toAddr, msg.Subject, dateStr, len(msg.Raw), msg.Raw,
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (s *InboundSession) Reset() {
	s.from = ""
	s.to = nil
}

func (s *InboundSession) Logout() error {
	return nil
}
