package imap

import (
	"fmt"

	"github.com/emersion/go-imap/backend"

	"gogomail/internal/db"
)

// User implements the go-imap backend.User interface.
type User struct {
	db   *db.DB
	id   int64
	name string
}

func (u *User) Username() string {
	return u.name
}

func (u *User) ListMailboxes(subscribed bool) ([]backend.Mailbox, error) {
	rows, err := u.db.Reader.Query(
		"SELECT id, name, uid_validity, uid_next FROM mailboxes WHERE user_id = ? ORDER BY name",
		u.id,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var mailboxes []backend.Mailbox
	for rows.Next() {
		m := &Mailbox{db: u.db, userID: u.id}
		if err := rows.Scan(&m.id, &m.name, &m.uidValidity, &m.uidNext); err != nil {
			return nil, err
		}
		mailboxes = append(mailboxes, m)
	}
	return mailboxes, nil
}

func (u *User) GetMailbox(name string) (backend.Mailbox, error) {
	m := &Mailbox{db: u.db, userID: u.id}
	err := u.db.Reader.QueryRow(
		"SELECT id, name, uid_validity, uid_next FROM mailboxes WHERE user_id = ? AND name = ?",
		u.id, name,
	).Scan(&m.id, &m.name, &m.uidValidity, &m.uidNext)
	if err != nil {
		return nil, backend.ErrNoSuchMailbox
	}
	return m, nil
}

func (u *User) CreateMailbox(name string) error {
	_, err := u.db.Writer.Exec(
		"INSERT INTO mailboxes (user_id, name) VALUES (?, ?)",
		u.id, name,
	)
	if err != nil {
		return fmt.Errorf("create mailbox: %w", err)
	}
	return nil
}

func (u *User) DeleteMailbox(name string) error {
	if name == "INBOX" {
		return fmt.Errorf("cannot delete INBOX")
	}
	_, err := u.db.Writer.Exec(
		"DELETE FROM mailboxes WHERE user_id = ? AND name = ?",
		u.id, name,
	)
	return err
}

func (u *User) RenameMailbox(existingName, newName string) error {
	if existingName == "INBOX" {
		return fmt.Errorf("cannot rename INBOX")
	}
	_, err := u.db.Writer.Exec(
		"UPDATE mailboxes SET name = ? WHERE user_id = ? AND name = ?",
		newName, u.id, existingName,
	)
	return err
}

func (u *User) Logout() error {
	return nil
}
