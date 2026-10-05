// Package keygen creates new secret values for env4ci rotate.
package keygen

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Secret returns 32 random bytes, base64url without padding (43 characters).
func Secret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand.Read never fails (Go 1.24+)
	return base64.RawURLEncoding.EncodeToString(b)
}

// SSH returns a new ed25519 key: the OpenSSH private key (PEM) and the
// authorized_keys line for it. None of the steps can fail for ed25519 with
// crypto/rand, so errors are not returned.
func SSH(comment string) (private, authorized string) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	block, _ := ssh.MarshalPrivateKey(priv, comment)
	sp, _ := ssh.NewPublicKey(pub)
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sp)))
	if comment != "" {
		line += " " + comment
	}
	return strings.TrimSpace(string(pem.EncodeToMemory(block))), line
}
