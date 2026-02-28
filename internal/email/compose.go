package email

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"path/filepath"
	"strings"
	"time"

	"github.com/emersion/go-message/mail"
)

// generateMessageID returns an ID without angle brackets.
// SetMessageID wraps it in <>, and composeMultipart adds them manually.
func generateMessageID(from string) string {
	b := make([]byte, 16)
	rand.Read(b)
	domain := "localhost"
	if parts := strings.SplitN(from, "@", 2); len(parts) == 2 {
		domain = parts[1]
	}
	return fmt.Sprintf("%x@%s", b, domain)
}

// Attachment holds a file to be attached to an email.
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// ComposeParams holds the fields for building a new email message.
type ComposeParams struct {
	From        string
	To          string
	Subject     string
	Body        string
	Attachments []Attachment
}

// Compose builds an RFC 5322 message from the given parameters.
// If attachments are present it produces a multipart/mixed message,
// otherwise a simple text/plain message.
func Compose(p ComposeParams) ([]byte, error) {
	if len(p.Attachments) == 0 {
		return composeSimple(p)
	}
	return composeMultipart(p)
}

func composeSimple(p ComposeParams) ([]byte, error) {
	var buf bytes.Buffer

	var h mail.Header
	h.SetMessageID(generateMessageID(p.From))
	h.SetDate(time.Now())
	h.SetSubject(p.Subject)
	h.SetAddressList("From", []*mail.Address{{Address: p.From}})
	h.SetAddressList("To", []*mail.Address{{Address: p.To}})
	h.Set("MIME-Version", "1.0")
	h.SetContentType("text/plain", map[string]string{"charset": "utf-8"})

	w, err := mail.CreateSingleInlineWriter(&buf, h)
	if err != nil {
		return nil, fmt.Errorf("create writer: %w", err)
	}

	if _, err := w.Write([]byte(p.Body)); err != nil {
		return nil, fmt.Errorf("write body: %w", err)
	}

	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("close writer: %w", err)
	}

	return buf.Bytes(), nil
}

func composeMultipart(p ComposeParams) ([]byte, error) {
	var buf bytes.Buffer

	// Write top-level headers manually for multipart
	fmt.Fprintf(&buf, "Message-ID: <%s>\r\n", generateMessageID(p.From))
	fmt.Fprintf(&buf, "From: %s\r\n", p.From)
	fmt.Fprintf(&buf, "To: %s\r\n", p.To)
	fmt.Fprintf(&buf, "Subject: %s\r\n", p.Subject)
	fmt.Fprintf(&buf, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")

	mpw := multipart.NewWriter(&buf)
	fmt.Fprintf(&buf, "Content-Type: multipart/mixed; boundary=%s\r\n", mpw.Boundary())
	fmt.Fprintf(&buf, "\r\n")

	// Text body part
	textHeader := make(textproto.MIMEHeader)
	textHeader.Set("Content-Type", "text/plain; charset=utf-8")
	pw, err := mpw.CreatePart(textHeader)
	if err != nil {
		return nil, fmt.Errorf("create text part: %w", err)
	}
	if _, err := io.WriteString(pw, p.Body); err != nil {
		return nil, fmt.Errorf("write text part: %w", err)
	}

	// Attachment parts
	for _, att := range p.Attachments {
		ct := att.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		attHeader := make(textproto.MIMEHeader)
		attHeader.Set("Content-Type", ct)
		attHeader.Set("Content-Transfer-Encoding", "base64")
		attHeader.Set("Content-Disposition",
			mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(att.Filename)}))

		pw, err := mpw.CreatePart(attHeader)
		if err != nil {
			return nil, fmt.Errorf("create attachment part: %w", err)
		}

		encoder := base64.NewEncoder(base64.StdEncoding, pw)
		if _, err := encoder.Write(att.Data); err != nil {
			return nil, fmt.Errorf("encode attachment: %w", err)
		}
		encoder.Close()
	}

	if err := mpw.Close(); err != nil {
		return nil, fmt.Errorf("close multipart: %w", err)
	}

	return buf.Bytes(), nil
}
