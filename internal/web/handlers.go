package web

import (
	"database/sql"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"picomailgo/internal/auth"
	"picomailgo/internal/email"
)

// Template data types

type MailboxInfo struct {
	Name string
}

type MessageSummary struct {
	ID            int64
	From          string
	Subject       string
	DateFormatted string
	Seen          bool
}

type MessageDetail struct {
	ID            int64
	From          string
	To            string
	Subject       string
	DateFormatted string
	Body          template.HTML
}

// Auth handlers

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, "login.html", map[string]any{"Title": "Login"})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")

	user, err := s.auth.Authenticate(username, password)
	if err != nil {
		s.render(w, "login.html", map[string]any{
			"Title": "Login",
			"Error": "Invalid username or password",
		})
		return
	}

	token, err := s.auth.GenerateToken(user)
	if err != nil {
		s.render(w, "login.html", map[string]any{
			"Title": "Login",
			"Error": "Internal error",
		})
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "token",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400,
	})
	http.Redirect(w, r, "/mailbox/INBOX", http.StatusSeeOther)
}

func (s *Server) handleRegisterPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, "register.html", map[string]any{"Title": "Register"})
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	confirm := r.FormValue("password_confirm")

	if username == "" || password == "" {
		s.render(w, "register.html", map[string]any{
			"Title": "Register",
			"Error": "Username and password are required",
		})
		return
	}

	if len(password) < 8 {
		s.render(w, "register.html", map[string]any{
			"Title": "Register",
			"Error": "Password must be at least 8 characters",
		})
		return
	}

	if password != confirm {
		s.render(w, "register.html", map[string]any{
			"Title": "Register",
			"Error": "Passwords do not match",
		})
		return
	}

	user, err := s.auth.CreateUser(username, password)
	if err != nil {
		msg := "Registration failed"
		if err == auth.ErrUserExists {
			msg = "Username already taken"
		}
		s.render(w, "register.html", map[string]any{
			"Title": "Register",
			"Error": msg,
		})
		return
	}

	token, err := s.auth.GenerateToken(user)
	if err != nil {
		s.render(w, "register.html", map[string]any{
			"Title": "Register",
			"Error": "Internal error",
		})
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "token",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400,
	})
	http.Redirect(w, r, "/mailbox/INBOX", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:   "token",
		MaxAge: -1,
		Path:   "/",
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// Protected page handlers

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/mailbox/INBOX", http.StatusSeeOther)
}

func (s *Server) handleMailbox(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	mailboxName := r.PathValue("name")

	mailboxes, err := s.getUserMailboxes(user.ID)
	if err != nil {
		http.Error(w, "Failed to load mailboxes", http.StatusInternalServerError)
		return
	}

	messages, hasMore, err := s.getMessages(user.ID, mailboxName, 0, 50)
	if err != nil {
		http.Error(w, "Failed to load messages", http.StatusInternalServerError)
		return
	}

	s.render(w, "inbox.html", map[string]any{
		"Title":         mailboxName,
		"User":          user,
		"Mailbox":       mailboxName,
		"ActiveMailbox": mailboxName,
		"Mailboxes":     mailboxes,
		"Messages":      messages,
		"HasMore":       hasMore,
		"NextOffset":    50,
	})
}

func (s *Server) handleMessage(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	idStr := r.PathValue("id")
	msgID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	msg, mailboxName, err := s.getMessage(user.ID, msgID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	s.markSeen(msgID)

	mailboxes, _ := s.getUserMailboxes(user.ID)

	s.render(w, "message.html", map[string]any{
		"Title":         msg.Subject,
		"User":          user,
		"Message":       msg,
		"Mailbox":       mailboxName,
		"ActiveMailbox": mailboxName,
		"Mailboxes":     mailboxes,
	})
}

func (s *Server) handleComposePage(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	mailboxes, _ := s.getUserMailboxes(user.ID)

	to := r.URL.Query().Get("to")
	subject := r.URL.Query().Get("subject")
	body := r.URL.Query().Get("body")

	// Reply prefill
	if replyID := r.URL.Query().Get("reply"); replyID != "" {
		if msgID, err := strconv.ParseInt(replyID, 10, 64); err == nil {
			if orig, _, err := s.getMessage(user.ID, msgID); err == nil {
				to = orig.From
				subject = "Re: " + orig.Subject
				textBody, _ := s.getMessageTextBody(msgID, user.ID)
				body = "\n\nOn " + orig.DateFormatted + ", " + orig.From + " wrote:\n> " +
					strings.ReplaceAll(strings.TrimRight(textBody, "\n"), "\n", "\n> ")
			}
		}
	}

	// Forward prefill
	if fwdID := r.URL.Query().Get("forward"); fwdID != "" {
		if msgID, err := strconv.ParseInt(fwdID, 10, 64); err == nil {
			if orig, _, err := s.getMessage(user.ID, msgID); err == nil {
				subject = "Fwd: " + orig.Subject
				textBody, _ := s.getMessageTextBody(msgID, user.ID)
				body = "\n\n---------- Forwarded message ----------\n" +
					"From: " + orig.From + "\n" +
					"Date: " + orig.DateFormatted + "\n" +
					"Subject: " + orig.Subject + "\n" +
					"To: " + orig.To + "\n\n" +
					textBody
			}
		}
	}

	fromAddr := user.Username + "@" + s.cfg.Server.Domain
	s.render(w, "compose.html", map[string]any{
		"Title":         "Compose",
		"User":          user,
		"ActiveMailbox": "",
		"Mailboxes":     mailboxes,
		"From":          fromAddr,
		"To":            to,
		"Subject":       subject,
		"Body":          body,
	})
}

func (s *Server) handleCompose(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	mailboxes, _ := s.getUserMailboxes(user.ID)

	// Parse multipart form (32MB max for attachments)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		r.ParseForm()
	}

	to := strings.TrimSpace(r.FormValue("to"))
	subject := r.FormValue("subject")
	body := r.FormValue("body")

	fromAddr := user.Username + "@" + s.cfg.Server.Domain
	composeData := func(extra map[string]any) map[string]any {
		data := map[string]any{
			"Title": "Compose", "User": user, "ActiveMailbox": "",
			"Mailboxes": mailboxes, "From": fromAddr, "To": to, "Subject": subject, "Body": body,
		}
		for k, v := range extra {
			data[k] = v
		}
		return data
	}

	if to == "" {
		s.render(w, "compose.html", composeData(map[string]any{"Error": "Recipient is required"}))
		return
	}

	// Collect attachments
	var attachments []email.Attachment
	if r.MultipartForm != nil {
		for _, fh := range r.MultipartForm.File["attachments"] {
			f, err := fh.Open()
			if err != nil {
				continue
			}
			data, err := io.ReadAll(f)
			f.Close()
			if err != nil {
				continue
			}
			ct := fh.Header.Get("Content-Type")
			if ct == "" {
				ct = "application/octet-stream"
			}
			attachments = append(attachments, email.Attachment{
				Filename:    fh.Filename,
				ContentType: ct,
				Data:        data,
			})
		}
	}

	raw, err := email.Compose(email.ComposeParams{
		From:        fromAddr,
		To:          to,
		Subject:     subject,
		Body:        body,
		Attachments: attachments,
	})
	if err != nil {
		slog.Error("compose: build message failed", "error", err)
		s.render(w, "compose.html", composeData(map[string]any{"Error": "Failed to build message"}))
		return
	}

	if err := s.mail.Send(fromAddr, to, raw); err != nil {
		slog.Warn("compose: send failed", "error", err)
	}

	s.mail.SaveToSent(user.ID, fromAddr, to, subject, time.Now(), raw)

	s.render(w, "compose.html", map[string]any{
		"Title":         "Compose",
		"User":          user,
		"ActiveMailbox": "",
		"Mailboxes":     mailboxes,
		"From":          fromAddr,
		"Success":       "Message sent",
		"To":            "",
		"Subject":       "",
		"Body":          "",
	})
}

func (s *Server) handleSettingsPage(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	fullUser, _ := s.auth.GetUser(user.ID)
	mailboxes, _ := s.getUserMailboxes(user.ID)

	s.render(w, "settings.html", map[string]any{
		"Title":         "Settings",
		"User":          user,
		"ActiveMailbox": "",
		"Mailboxes":     mailboxes,
		"DisplayName":   fullUser.DisplayName,
	})
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	mailboxes, _ := s.getUserMailboxes(user.ID)

	displayName := strings.TrimSpace(r.FormValue("display_name"))
	currentPass := r.FormValue("current_password")
	newPass := r.FormValue("new_password")

	renderErr := func(msg string) {
		s.render(w, "settings.html", map[string]any{
			"Title":         "Settings",
			"User":          user,
			"ActiveMailbox": "",
			"Mailboxes":     mailboxes,
			"DisplayName":   displayName,
			"Error":         msg,
		})
	}

	if displayName != "" {
		if err := s.auth.UpdateDisplayName(user.ID, displayName); err != nil {
			renderErr("Failed to update display name")
			return
		}
	}

	if newPass != "" {
		if currentPass == "" {
			renderErr("Current password is required to change password")
			return
		}
		if _, err := s.auth.Authenticate(user.Username, currentPass); err != nil {
			renderErr("Current password is incorrect")
			return
		}
		if err := s.auth.ChangePassword(user.ID, newPass); err != nil {
			renderErr("Failed to change password")
			return
		}
	}

	s.render(w, "settings.html", map[string]any{
		"Title":         "Settings",
		"User":          user,
		"ActiveMailbox": "",
		"Mailboxes":     mailboxes,
		"DisplayName":   displayName,
		"Success":       "Settings saved",
	})
}

// DB query helpers

func (s *Server) getUserMailboxes(userID int64) ([]MailboxInfo, error) {
	rows, err := s.db.Reader.Query(
		`SELECT name FROM mailboxes WHERE user_id = ?
		 ORDER BY CASE name WHEN 'INBOX' THEN 0 WHEN 'Sent' THEN 1 WHEN 'Drafts' THEN 2 WHEN 'Trash' THEN 3 ELSE 4 END, name`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []MailboxInfo
	for rows.Next() {
		var m MailboxInfo
		if err := rows.Scan(&m.Name); err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	return result, nil
}

func (s *Server) getMessages(userID int64, mailbox string, offset, limit int) ([]MessageSummary, bool, error) {
	rows, err := s.db.Reader.Query(`
		SELECT m.id, m.from_addr, m.subject, m.date,
			EXISTS(SELECT 1 FROM message_flags mf WHERE mf.message_id = m.id AND mf.flag = '\Seen') as seen
		FROM messages m
		JOIN mailboxes mb ON m.mailbox_id = mb.id
		WHERE mb.user_id = ? AND mb.name = ?
		ORDER BY m.date DESC
		LIMIT ? OFFSET ?`,
		userID, mailbox, limit+1, offset,
	)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	var messages []MessageSummary
	for rows.Next() {
		var m MessageSummary
		var date sql.NullTime
		if err := rows.Scan(&m.ID, &m.From, &m.Subject, &date, &m.Seen); err != nil {
			return nil, false, err
		}
		if date.Valid {
			m.DateFormatted = date.Time.Format("Jan 2, 15:04")
		}
		messages = append(messages, m)
	}

	hasMore := len(messages) > limit
	if hasMore {
		messages = messages[:limit]
	}
	return messages, hasMore, nil
}

func (s *Server) getMessage(userID int64, msgID int64) (*MessageDetail, string, error) {
	var msg MessageDetail
	var date sql.NullTime
	var rawBody []byte
	var mailboxName string

	err := s.db.Reader.QueryRow(`
		SELECT m.id, m.from_addr, m.to_addr, m.subject, m.date, m.raw, mb.name
		FROM messages m
		JOIN mailboxes mb ON m.mailbox_id = mb.id
		WHERE m.id = ? AND mb.user_id = ?`,
		msgID, userID,
	).Scan(&msg.ID, &msg.From, &msg.To, &msg.Subject, &date, &rawBody, &mailboxName)
	if err != nil {
		return nil, "", err
	}

	if date.Valid {
		msg.DateFormatted = date.Time.Format("Mon, Jan 2, 2006 at 3:04 PM")
	}

	// Extract plain text body from MIME message
	textBody, err := email.GetTextBody(rawBody)
	if err != nil || textBody == "" {
		textBody = string(rawBody)
	}
	msg.Body = template.HTML("<pre>" + template.HTMLEscapeString(textBody) + "</pre>")

	return &msg, mailboxName, nil
}

func (s *Server) markSeen(msgID int64) {
	s.db.Writer.Exec(`INSERT OR IGNORE INTO message_flags (message_id, flag) VALUES (?, '\Seen')`, msgID)
}

func (s *Server) getMessageTextBody(msgID int64, userID int64) (string, error) {
	var raw []byte
	err := s.db.Reader.QueryRow(`
		SELECT m.raw FROM messages m
		JOIN mailboxes mb ON m.mailbox_id = mb.id
		WHERE m.id = ? AND mb.user_id = ?`,
		msgID, userID,
	).Scan(&raw)
	if err != nil {
		return "", err
	}
	body, err := email.GetTextBody(raw)
	if err != nil || body == "" {
		return string(raw), nil
	}
	return body, nil
}
