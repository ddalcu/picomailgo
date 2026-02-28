package web

import (
	"net/http"
	"strconv"

	"gogomail/internal/auth"
)

func (s *Server) handleMessageList(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	mailbox := r.URL.Query().Get("mailbox")
	offsetStr := r.URL.Query().Get("offset")

	offset, _ := strconv.Atoi(offsetStr)
	if offset < 0 {
		offset = 0
	}

	messages, hasMore, err := s.getMessages(user.ID, mailbox, offset, 50)
	if err != nil {
		http.Error(w, "Failed to load messages", http.StatusInternalServerError)
		return
	}

	s.render(w, "partials/message-list.html", map[string]any{
		"Messages":      messages,
		"HasMore":       hasMore,
		"ActiveMailbox": mailbox,
		"NextOffset":    offset + 50,
	})
}

func (s *Server) handleDeleteMessage(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	idStr := r.PathValue("id")
	msgID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid message ID", http.StatusBadRequest)
		return
	}

	// Verify the message belongs to the user
	var mailboxName string
	err = s.db.Reader.QueryRow(`
		SELECT mb.name FROM messages m
		JOIN mailboxes mb ON m.mailbox_id = mb.id
		WHERE m.id = ? AND mb.user_id = ?`,
		msgID, user.ID,
	).Scan(&mailboxName)
	if err != nil {
		http.Error(w, "Message not found", http.StatusNotFound)
		return
	}

	if mailboxName == "Trash" {
		// Permanently delete from Trash
		s.db.Writer.Exec("DELETE FROM messages WHERE id = ?", msgID)
	} else {
		// Move to Trash
		var trashID int64
		err = s.db.Reader.QueryRow(
			"SELECT id FROM mailboxes WHERE user_id = ? AND name = 'Trash'",
			user.ID,
		).Scan(&trashID)
		if err != nil {
			http.Error(w, "Trash folder not found", http.StatusInternalServerError)
			return
		}

		// Get next UID for Trash
		var uid int64
		s.db.Writer.QueryRow("UPDATE mailboxes SET uid_next = uid_next + 1 WHERE id = ? RETURNING uid_next - 1", trashID).Scan(&uid)
		s.db.Writer.Exec("UPDATE messages SET mailbox_id = ?, uid = ? WHERE id = ?", trashID, uid, msgID)
	}

	// Redirect back to mailbox
	w.Header().Set("HX-Redirect", "/mailbox/"+mailboxName)
	w.WriteHeader(http.StatusOK)
}
