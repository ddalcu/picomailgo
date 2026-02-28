package smtp

import (
	cryptotls "crypto/tls"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/smtp"
	"strings"
	"time"

	"picomailgo/internal/config"
	"picomailgo/internal/db"
	"picomailgo/internal/email"
)

// Relay handles outbound email delivery via MX lookup.
type Relay struct {
	cfg    *config.Config
	db     *db.DB
	signer *DKIMSigner
}

func NewRelay(cfg *config.Config, database *db.DB) *Relay {
	return &Relay{
		cfg:    cfg,
		db:     database,
		signer: NewDKIMSigner(database),
	}
}

// Send delivers a message locally if the recipient is on our domain,
// otherwise signs with DKIM and relays via MX lookup.
func (r *Relay) Send(from, to string, raw []byte) error {
	parts := strings.SplitN(to, "@", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid address: %s", to)
	}
	rcptDomain := parts[1]

	// Local delivery — no MX lookup needed
	if strings.EqualFold(rcptDomain, r.cfg.Server.Domain) {
		return r.deliverLocal(from, to, parts[0], raw)
	}

	// DKIM sign for outbound
	signed, err := r.signer.Sign(raw, r.cfg.Server.Domain)
	if err != nil {
		slog.Warn("relay: DKIM sign failed, sending unsigned", "error", err)
		signed = raw
	}

	// Try immediate delivery
	if err := r.deliver(from, to, signed); err != nil {
		slog.Warn("relay: immediate delivery failed, queuing", "to", to, "error", err)
		return r.enqueue(from, to, signed, err.Error())
	}

	slog.Info("relay: delivered message", "from", from, "to", to)
	return nil
}

// deliverLocal delivers a message to a local user's INBOX.
func (r *Relay) deliverLocal(from, to, username string, raw []byte) error {
	var exists int
	r.db.Reader.QueryRow("SELECT COUNT(*) FROM users WHERE username = ?", username).Scan(&exists)
	if exists == 0 {
		slog.Warn("relay: rejected local delivery, user not found", "from", from, "to", to)
		return fmt.Errorf("user not found: %s", to)
	}

	parsed, err := email.ParseHeaders(raw)
	if err != nil {
		parsed = &email.ParsedMessage{
			From:    from,
			Subject: "(no subject)",
			Date:    time.Now(),
		}
	}
	parsed.Raw = raw
	if parsed.From == "" {
		parsed.From = from
	}

	if err := deliverToInbox(r.db, username, to, parsed); err != nil {
		return fmt.Errorf("local delivery to %s: %w", to, err)
	}
	slog.Info("relay: delivered locally", "from", from, "to", to)
	return nil
}

// deliver attempts direct SMTP delivery via MX lookup.
func (r *Relay) deliver(from, to string, raw []byte) error {
	domain := strings.SplitN(to, "@", 2)[1]

	mxRecords, err := net.LookupMX(domain)
	if err != nil {
		return fmt.Errorf("MX lookup for %s: %w", domain, err)
	}

	if len(mxRecords) == 0 {
		// Fall back to A record
		mxRecords = []*net.MX{{Host: domain, Pref: 0}}
	}

	var lastErr error
	for _, mx := range mxRecords {
		host := strings.TrimSuffix(mx.Host, ".")
		addr := host + ":25"

		if err := r.deliverToHost(addr, host, from, to, raw); err != nil {
			lastErr = err
			slog.Warn("relay: MX delivery failed", "to", to, "host", host, "error", err)
			continue
		}
		return nil
	}

	return fmt.Errorf("all MX hosts failed for %s: %v", domain, lastErr)
}

// deliverToHost connects to a single MX host with proper EHLO and STARTTLS.
func (r *Relay) deliverToHost(addr, host, from, to string, raw []byte) error {
	c, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer c.Close()

	// Use our actual hostname for EHLO
	if err := c.Hello(r.cfg.Server.Domain); err != nil {
		return fmt.Errorf("EHLO: %w", err)
	}

	// Upgrade to TLS if supported
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&cryptotls.Config{ServerName: host}); err != nil {
			slog.Warn("relay: STARTTLS failed, continuing unencrypted", "host", host, "error", err)
		}
	}

	if err := c.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("RCPT TO: %w", err)
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		return fmt.Errorf("write data: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close data: %w", err)
	}

	return c.Quit()
}

// enqueue adds a message to the outbound queue for retry.
func (r *Relay) enqueue(from, to string, raw []byte, lastError string) error {
	_, err := r.db.Writer.Exec(`
		INSERT INTO outbound_queue (from_addr, to_addr, raw, next_retry, last_error)
		VALUES (?, ?, ?, datetime('now', '+5 minutes'), ?)`,
		from, to, raw, lastError,
	)
	return err
}

// ProcessQueue attempts to deliver queued messages. Call this periodically.
func (r *Relay) ProcessQueue() {
	rows, err := r.db.Reader.Query(`
		SELECT id, from_addr, to_addr, raw, attempts, max_attempts
		FROM outbound_queue
		WHERE next_retry <= datetime('now') AND attempts < max_attempts
		ORDER BY next_retry
		LIMIT 50`)
	if err != nil {
		slog.Error("relay: query queue", "error", err)
		return
	}
	defer rows.Close()

	type queueItem struct {
		id, attempts, maxAttempts int64
		from, to                 string
		raw                      []byte
	}

	var items []queueItem
	for rows.Next() {
		var item queueItem
		if err := rows.Scan(&item.id, &item.from, &item.to, &item.raw, &item.attempts, &item.maxAttempts); err != nil {
			continue
		}
		items = append(items, item)
	}

	for _, item := range items {
		if err := r.deliver(item.from, item.to, item.raw); err != nil {
			newAttempts := item.attempts + 1
			if newAttempts >= item.maxAttempts {
				slog.Warn("relay: giving up on message", "id", item.id, "to", item.to, "attempts", newAttempts)
				r.db.Writer.Exec("DELETE FROM outbound_queue WHERE id = ?", item.id)
				continue
			}

			// Exponential backoff: 5min, 15min, 45min, 2h, 6h, 18h, etc.
			backoffMin := int(5 * math.Pow(3, float64(newAttempts-1)))
			r.db.Writer.Exec(`
				UPDATE outbound_queue
				SET attempts = ?, next_retry = datetime('now', ? || ' minutes'), last_error = ?
				WHERE id = ?`,
				newAttempts, fmt.Sprintf("+%d", backoffMin), err.Error(), item.id,
			)
		} else {
			r.db.Writer.Exec("DELETE FROM outbound_queue WHERE id = ?", item.id)
			slog.Info("relay: delivered queued message", "id", item.id, "to", item.to)
		}
	}
}

// StartQueueProcessor runs the queue processor in a loop.
func (r *Relay) StartQueueProcessor(stop <-chan struct{}) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.ProcessQueue()
		case <-stop:
			return
		}
	}
}

// SaveToSent stores a copy of the sent message in the user's Sent folder.
func (r *Relay) SaveToSent(userID int64, from, to, subject string, date time.Time, raw []byte) error {
	tx, err := r.db.Writer.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var mailboxID, uid int64
	err = tx.QueryRow(`
		UPDATE mailboxes SET uid_next = uid_next + 1
		WHERE name = 'Sent' AND user_id = ?
		RETURNING id, uid_next - 1`,
		userID,
	).Scan(&mailboxID, &uid)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}

	dateStr := date.UTC().Format("2006-01-02 15:04:05")
	_, err = tx.Exec(`
		INSERT INTO messages (mailbox_id, uid, from_addr, to_addr, subject, date, size, raw)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		mailboxID, uid, from, to, subject, dateStr, len(raw), raw,
	)
	if err != nil {
		return err
	}

	// Mark as seen
	res, _ := tx.Exec("SELECT last_insert_rowid()")
	if msgID, err := res.LastInsertId(); err == nil {
		tx.Exec(`INSERT OR IGNORE INTO message_flags (message_id, flag) VALUES (?, '\Seen')`, msgID)
	}

	return tx.Commit()
}
