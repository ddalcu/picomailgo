package imap

import (
	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/backend"

	"picomailgo/internal/auth"
	"picomailgo/internal/db"
)

// Backend implements the go-imap backend.Backend interface.
type Backend struct {
	db      *db.DB
	authSvc *auth.Service
}

func NewBackend(database *db.DB, authSvc *auth.Service) *Backend {
	return &Backend{db: database, authSvc: authSvc}
}

func (b *Backend) Login(_ *imap.ConnInfo, username, password string) (backend.User, error) {
	user, err := b.authSvc.Authenticate(username, password)
	if err != nil {
		return nil, backend.ErrInvalidCredentials
	}

	return &User{
		db:   b.db,
		id:   user.ID,
		name: user.Username,
	}, nil
}
