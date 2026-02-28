package smtp

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"

	"github.com/emersion/go-msgauth/dkim"

	"gogomail/internal/db"
)

// DKIMSigner signs outbound messages with DKIM.
type DKIMSigner struct {
	db *db.DB
}

func NewDKIMSigner(database *db.DB) *DKIMSigner {
	return &DKIMSigner{db: database}
}

// Sign applies DKIM signature to the raw message for the given domain.
// Returns the signed message bytes, or the original if no key is configured.
func (s *DKIMSigner) Sign(raw []byte, domain string) ([]byte, error) {
	var privKeyPEM []byte
	var selector string

	err := s.db.Reader.QueryRow(
		"SELECT selector, private_key FROM dkim_keys WHERE domain = ? ORDER BY created_at DESC LIMIT 1",
		domain,
	).Scan(&selector, &privKeyPEM)
	if err != nil {
		// No DKIM key for this domain — return unsigned
		return raw, nil
	}

	block, _ := pem.Decode(privKeyPEM)
	if block == nil {
		return nil, fmt.Errorf("dkim: invalid PEM key for %s", domain)
	}

	privKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("dkim: parse key for %s: %w", domain, err)
	}

	opts := &dkim.SignOptions{
		Domain:   domain,
		Selector: selector,
		Signer:   privKey,
		Hash:     crypto.SHA256,
		HeaderKeys: []string{
			"From", "To", "Subject", "Date", "Message-ID", "MIME-Version", "Content-Type",
		},
	}

	var signed bytes.Buffer
	if err := dkim.Sign(&signed, bytes.NewReader(raw), opts); err != nil {
		return nil, fmt.Errorf("dkim: sign: %w", err)
	}

	return signed.Bytes(), nil
}

// GenerateKey creates a new DKIM key pair and stores it in the database.
func (s *DKIMSigner) GenerateKey(domain, selector string) (publicKeyDNS string, err error) {
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", fmt.Errorf("generate RSA key: %w", err)
	}

	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privKey),
	})

	pubDER, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	if err != nil {
		return "", fmt.Errorf("marshal public key: %w", err)
	}

	pubPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubDER,
	})

	// DNS TXT record value
	pubB64 := pem.EncodeToMemory(&pem.Block{Bytes: pubDER})
	// Strip PEM headers for DNS record
	dnsValue := fmt.Sprintf("v=DKIM1; k=rsa; p=%s", stripPEM(string(pubB64)))

	_, err = s.db.Writer.Exec(
		"INSERT OR REPLACE INTO dkim_keys (domain, selector, private_key, public_key) VALUES (?, ?, ?, ?)",
		domain, selector, privPEM, string(pubPEM),
	)
	if err != nil {
		return "", fmt.Errorf("store key: %w", err)
	}

	return dnsValue, nil
}

func stripPEM(s string) string {
	var result []byte
	for _, line := range bytes.Split([]byte(s), []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] == '-' {
			continue
		}
		result = append(result, line...)
	}
	return string(result)
}
