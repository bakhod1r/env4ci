package keygen

import (
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestSecret(t *testing.T) {
	a, b := Secret(), Secret()
	if len(a) != 43 || a == b || strings.ContainsAny(a, "+/=") {
		t.Fatalf("%q %q", a, b)
	}
}

func TestSSH(t *testing.T) {
	priv, line := SSH("env4ci DEPLOY_SSH_KEY")
	signer, err := ssh.ParsePrivateKey([]byte(priv))
	if err != nil {
		t.Fatal(err)
	}
	pub, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil || comment != "env4ci DEPLOY_SSH_KEY" || string(pub.Marshal()) != string(signer.PublicKey().Marshal()) {
		t.Fatalf("line %q: %v", line, err)
	}
	if !strings.HasPrefix(priv, "-----BEGIN OPENSSH PRIVATE KEY-----") {
		t.Fatal(priv[:30])
	}
	if _, line := SSH(""); strings.Count(line, " ") != 1 {
		t.Fatalf("no comment: %q", line)
	}
}
