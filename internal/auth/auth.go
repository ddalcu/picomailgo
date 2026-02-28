package auth

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUserExists         = errors.New("user already exists")
)

type Service struct {
	db        *sql.DB
	reader    *sql.DB
	jwtSecret []byte
}

type User struct {
	ID          int64
	Username    string
	DisplayName string
}

type Claims struct {
	UserID   int64  `json:"uid"`
	Username string `json:"usr"`
	jwt.RegisteredClaims
}

func NewService(writer, reader *sql.DB, jwtSecret string) *Service {
	return &Service{
		db:        writer,
		reader:    reader,
		jwtSecret: []byte(jwtSecret),
	}
}

// CreateUser registers a new user and creates default mailboxes.
func (s *Service) CreateUser(username, password string) (*User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		"INSERT INTO users (username, password_hash, display_name) VALUES (?, ?, ?)",
		username, string(hash), username,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrUserExists
		}
		return nil, fmt.Errorf("insert user: %w", err)
	}

	userID, _ := res.LastInsertId()

	for _, mbox := range []string{"INBOX", "Sent", "Drafts", "Trash"} {
		if _, err := tx.Exec(
			"INSERT INTO mailboxes (user_id, name) VALUES (?, ?)",
			userID, mbox,
		); err != nil {
			return nil, fmt.Errorf("create mailbox %s: %w", mbox, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return &User{ID: userID, Username: username, DisplayName: username}, nil
}

// Authenticate verifies credentials and returns the user.
func (s *Service) Authenticate(username, password string) (*User, error) {
	var u User
	var hash string
	err := s.reader.QueryRow(
		"SELECT id, username, display_name, password_hash FROM users WHERE username = ?",
		username,
	).Scan(&u.ID, &u.Username, &u.DisplayName, &hash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("query user: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}

	return &u, nil
}

// GenerateToken creates a JWT for the given user.
func (s *Service) GenerateToken(user *User) (string, error) {
	claims := Claims{
		UserID:   user.ID,
		Username: user.Username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.jwtSecret)
}

// ValidateToken parses and validates a JWT, returning the claims.
func (s *Service) ValidateToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.jwtSecret, nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}

	return claims, nil
}

// ChangePassword updates the password for the given user ID.
func (s *Service) ChangePassword(userID int64, newPassword string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	_, err = s.db.Exec("UPDATE users SET password_hash = ?, updated_at = datetime('now') WHERE id = ?",
		string(hash), userID)
	return err
}

// UpdateDisplayName sets the display name for the given user ID.
func (s *Service) UpdateDisplayName(userID int64, name string) error {
	_, err := s.db.Exec("UPDATE users SET display_name = ?, updated_at = datetime('now') WHERE id = ?",
		name, userID)
	return err
}

// GetUser fetches a user by ID.
func (s *Service) GetUser(id int64) (*User, error) {
	var u User
	err := s.reader.QueryRow(
		"SELECT id, username, display_name FROM users WHERE id = ?", id,
	).Scan(&u.ID, &u.Username, &u.DisplayName)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func isUniqueViolation(err error) bool {
	return err != nil && (
		// modernc.org/sqlite error format
		contains(err.Error(), "UNIQUE constraint failed") ||
		contains(err.Error(), "constraint failed"))
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
