package imap

import (
	"bytes"
	"database/sql"
	"io"
	"time"

	"github.com/emersion/go-imap"

	"gogomail/internal/db"
)

// Mailbox implements the go-imap backend.Mailbox interface.
type Mailbox struct {
	db          *db.DB
	userID      int64
	id          int64
	name        string
	uidValidity uint32
	uidNext     uint32
}

func (m *Mailbox) Name() string {
	return m.name
}

func (m *Mailbox) Info() (*imap.MailboxInfo, error) {
	return &imap.MailboxInfo{
		Delimiter: "/",
		Name:      m.name,
	}, nil
}

func (m *Mailbox) Status(items []imap.StatusItem) (*imap.MailboxStatus, error) {
	status := imap.NewMailboxStatus(m.name, items)
	status.UidValidity = m.uidValidity
	status.UidNext = m.uidNext

	for _, item := range items {
		switch item {
		case imap.StatusMessages:
			var count uint32
			m.db.Reader.QueryRow("SELECT COUNT(*) FROM messages WHERE mailbox_id = ?", m.id).Scan(&count)
			status.Messages = count
		case imap.StatusRecent:
			status.Recent = 0
		case imap.StatusUnseen:
			var count uint32
			m.db.Reader.QueryRow(`
				SELECT COUNT(*) FROM messages m
				WHERE m.mailbox_id = ?
				AND NOT EXISTS(SELECT 1 FROM message_flags mf WHERE mf.message_id = m.id AND mf.flag = '\Seen')`,
				m.id,
			).Scan(&count)
			status.Unseen = count
		}
	}

	return status, nil
}

func (m *Mailbox) SetSubscribed(subscribed bool) error {
	return nil // all mailboxes are subscribed
}

func (m *Mailbox) Check() error {
	return nil
}

func (m *Mailbox) ListMessages(uid bool, seqSet *imap.SeqSet, items []imap.FetchItem, ch chan<- *imap.Message) error {
	defer close(ch)

	msgs, err := m.fetchMessages(uid, seqSet)
	if err != nil {
		return err
	}

	for _, msg := range msgs {
		fetched := imap.NewMessage(msg.seqNum, items)
		for _, item := range items {
			switch item {
			case imap.FetchEnvelope:
				fetched.Envelope = msg.envelope()
			case imap.FetchFlags:
				fetched.Flags = msg.flags
			case imap.FetchInternalDate:
				fetched.InternalDate = msg.date
			case imap.FetchRFC822Size:
				fetched.Size = msg.size
			case imap.FetchUid:
				fetched.Uid = msg.uid
			default:
				section, err := imap.ParseBodySectionName(item)
				if err != nil {
					continue
				}
				fetched.Body[section] = newLiteral(msg.raw)
			}
		}
		ch <- fetched
	}

	return nil
}

func (m *Mailbox) SearchMessages(uid bool, criteria *imap.SearchCriteria) ([]uint32, error) {
	// Basic search: return all message UIDs/sequence numbers
	rows, err := m.db.Reader.Query(
		"SELECT uid FROM messages WHERE mailbox_id = ? ORDER BY uid",
		m.id,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []uint32
	var seqNum uint32
	for rows.Next() {
		var u uint32
		if err := rows.Scan(&u); err != nil {
			continue
		}
		seqNum++
		if uid {
			ids = append(ids, u)
		} else {
			ids = append(ids, seqNum)
		}
	}
	return ids, nil
}

func (m *Mailbox) CreateMessage(flags []string, date time.Time, body imap.Literal) error {
	raw, err := io.ReadAll(body)
	if err != nil {
		return err
	}

	tx, err := m.db.Writer.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var uid int64
	err = tx.QueryRow(
		"UPDATE mailboxes SET uid_next = uid_next + 1 WHERE id = ? RETURNING uid_next - 1",
		m.id,
	).Scan(&uid)
	if err != nil {
		return err
	}

	dateStr := date.UTC().Format("2006-01-02 15:04:05")
	res, err := tx.Exec(`
		INSERT INTO messages (mailbox_id, uid, from_addr, to_addr, subject, date, size, raw)
		VALUES (?, ?, '', '', '', ?, ?, ?)`,
		m.id, uid, dateStr, len(raw), raw,
	)
	if err != nil {
		return err
	}

	msgID, _ := res.LastInsertId()
	for _, flag := range flags {
		tx.Exec("INSERT OR IGNORE INTO message_flags (message_id, flag) VALUES (?, ?)", msgID, flag)
	}

	return tx.Commit()
}

func (m *Mailbox) UpdateMessagesFlags(uid bool, seqSet *imap.SeqSet, op imap.FlagsOp, flags []string) error {
	msgs, err := m.fetchMessages(uid, seqSet)
	if err != nil {
		return err
	}

	for _, msg := range msgs {
		switch op {
		case imap.SetFlags:
			m.db.Writer.Exec("DELETE FROM message_flags WHERE message_id = ?", msg.id)
			for _, f := range flags {
				m.db.Writer.Exec("INSERT OR IGNORE INTO message_flags (message_id, flag) VALUES (?, ?)", msg.id, f)
			}
		case imap.AddFlags:
			for _, f := range flags {
				m.db.Writer.Exec("INSERT OR IGNORE INTO message_flags (message_id, flag) VALUES (?, ?)", msg.id, f)
			}
		case imap.RemoveFlags:
			for _, f := range flags {
				m.db.Writer.Exec("DELETE FROM message_flags WHERE message_id = ? AND flag = ?", msg.id, f)
			}
		}
	}

	return nil
}

func (m *Mailbox) CopyMessages(uid bool, seqSet *imap.SeqSet, dest string) error {
	msgs, err := m.fetchMessages(uid, seqSet)
	if err != nil {
		return err
	}

	var destID int64
	err = m.db.Reader.QueryRow(
		"SELECT id FROM mailboxes WHERE user_id = ? AND name = ?",
		m.userID, dest,
	).Scan(&destID)
	if err != nil {
		return err
	}

	for _, msg := range msgs {
		tx, err := m.db.Writer.Begin()
		if err != nil {
			return err
		}

		var newUID int64
		err = tx.QueryRow(
			"UPDATE mailboxes SET uid_next = uid_next + 1 WHERE id = ? RETURNING uid_next - 1",
			destID,
		).Scan(&newUID)
		if err != nil {
			tx.Rollback()
			return err
		}

		_, err = tx.Exec(`
			INSERT INTO messages (mailbox_id, uid, from_addr, to_addr, subject, date, size, raw)
			SELECT ?, ?, from_addr, to_addr, subject, date, size, raw FROM messages WHERE id = ?`,
			destID, newUID, msg.id,
		)
		if err != nil {
			tx.Rollback()
			return err
		}

		tx.Commit()
	}

	return nil
}

func (m *Mailbox) Expunge() error {
	_, err := m.db.Writer.Exec(`
		DELETE FROM messages WHERE mailbox_id = ? AND id IN (
			SELECT message_id FROM message_flags WHERE flag = '\Deleted'
		)`, m.id)
	return err
}

// Internal message type and helpers

type message struct {
	id     int64
	seqNum uint32
	uid    uint32
	from   string
	to     string
	subj   string
	date   time.Time
	size   uint32
	flags  []string
	raw    []byte
}

func (msg *message) envelope() *imap.Envelope {
	return &imap.Envelope{
		Date:    msg.date,
		Subject: msg.subj,
		From:    parseAddresses(msg.from),
		To:      parseAddresses(msg.to),
	}
}

func (m *Mailbox) fetchMessages(uid bool, seqSet *imap.SeqSet) ([]message, error) {
	// Get all messages for this mailbox with row numbers for sequence mapping
	rows, err := m.db.Reader.Query(`
		SELECT m.id, m.uid, m.from_addr, m.to_addr, m.subject, m.date, m.size, m.raw
		FROM messages m
		WHERE m.mailbox_id = ?
		ORDER BY m.uid`,
		m.id,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var all []message
	var seqNum uint32
	for rows.Next() {
		var msg message
		var dateStr sql.NullString
		if err := rows.Scan(&msg.id, &msg.uid, &msg.from, &msg.to, &msg.subj, &dateStr, &msg.size, &msg.raw); err != nil {
			continue
		}
		seqNum++
		msg.seqNum = seqNum

		if dateStr.Valid {
			msg.date, _ = time.Parse("2006-01-02 15:04:05", dateStr.String)
		}

		// Check if this message matches the sequence set
		if uid {
			if !seqSet.Contains(msg.uid) {
				continue
			}
		} else {
			if !seqSet.Contains(msg.seqNum) {
				continue
			}
		}

		all = append(all, msg)
	}

	// Load flags for matched messages
	for i := range all {
		all[i].flags = m.getFlags(all[i].id)
	}

	return all, nil
}

func (m *Mailbox) getFlags(messageID int64) []string {
	rows, err := m.db.Reader.Query(
		"SELECT flag FROM message_flags WHERE message_id = ?",
		messageID,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var flags []string
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err == nil {
			flags = append(flags, f)
		}
	}
	return flags
}

// literal wraps a byte slice to satisfy imap.Literal (io.Reader + Len).
type literal struct {
	*bytes.Reader
	len int
}

func newLiteral(b []byte) *literal {
	return &literal{Reader: bytes.NewReader(b), len: len(b)}
}

func (l *literal) Len() int {
	return l.len
}

func parseAddresses(s string) []*imap.Address {
	if s == "" {
		return nil
	}

	// Simple address parsing: split on comma
	parts := bytes.Split([]byte(s), []byte(","))
	var addrs []*imap.Address
	for _, p := range parts {
		addr := string(bytes.TrimSpace(p))
		if addr == "" {
			continue
		}
		// Split user@domain
		at := bytes.IndexByte([]byte(addr), '@')
		if at < 0 {
			addrs = append(addrs, &imap.Address{MailboxName: addr})
		} else {
			addrs = append(addrs, &imap.Address{
				MailboxName: addr[:at],
				HostName:    addr[at+1:],
			})
		}
	}
	return addrs
}
