// Package domain holds env4ci's pure business rules: variables, classification
// and sync plans. It has no I/O and no external dependencies.
package domain

import (
	"fmt"
	"regexp"
	"strings"
)

// Kind tells whether a variable is stored as a plain variable or an encrypted secret.
type Kind int

const (
	KindVariable Kind = iota
	KindSecret
)

func (k Kind) String() string {
	if k == KindSecret {
		return "secret"
	}
	return "variable"
}

// ParseKind converts "secret" / "variable" into a Kind.
func ParseKind(s string) (Kind, error) {
	switch strings.ToLower(s) {
	case "secret":
		return KindSecret, nil
	case "variable", "var":
		return KindVariable, nil
	}
	return 0, fmt.Errorf("unknown kind %q (want secret or variable)", s)
}

// Variable is one local key/value with its classification.
type Variable struct {
	Key   string
	Value string
	Kind  Kind
}

// Remote is a variable as seen on a provider. Known is false when the provider
// does not expose the value (e.g. GitHub secrets are write-only).
type Remote struct {
	Key   string
	Value string
	Kind  Kind
	Known bool
}

var keyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidateKey checks that a key is accepted by both GitHub and GitLab.
func ValidateKey(key string) error {
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("invalid key %q: must match %s", key, keyPattern)
	}
	if strings.HasPrefix(strings.ToUpper(key), "GITHUB_") {
		return fmt.Errorf("invalid key %q: GITHUB_ prefix is reserved", key)
	}
	return nil
}
