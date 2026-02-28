package email

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/emersion/go-message/mail"
)

// ParsedMessage holds extracted header fields from an RFC 5322 message.
type ParsedMessage struct {
	From    string
	To      string
	Subject string
	Date    time.Time
	Raw     []byte
}

// ParseHeaders extracts key headers from a raw RFC 5322 message.
func ParseHeaders(raw []byte) (*ParsedMessage, error) {
	r, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("create mail reader: %w", err)
	}
	defer r.Close()

	h := r.Header
	msg := &ParsedMessage{}

	if addrs, err := h.AddressList("From"); err == nil && len(addrs) > 0 {
		msg.From = addrs[0].Address
	}

	if addrs, err := h.AddressList("To"); err == nil && len(addrs) > 0 {
		var parts []string
		for _, a := range addrs {
			parts = append(parts, a.Address)
		}
		msg.To = strings.Join(parts, ", ")
	}

	msg.Subject, _ = h.Subject()

	if d, err := h.Date(); err == nil {
		msg.Date = d
	} else {
		msg.Date = time.Now()
	}

	return msg, nil
}

// GetTextBody reads the message parts and returns the plain text body.
func GetTextBody(raw []byte) (string, error) {
	r, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	defer r.Close()

	for {
		p, err := r.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}

		switch p.Header.(type) {
		case *mail.InlineHeader:
			ct := p.Header.Get("Content-Type")
			if strings.HasPrefix(ct, "text/plain") || ct == "" {
				body, err := io.ReadAll(p.Body)
				if err != nil {
					return "", err
				}
				return string(body), nil
			}
		}
	}

	return "", nil
}
