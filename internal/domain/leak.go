package domain

import (
	"bytes"
	"strings"
)

// Leak is a committed file line that contains a secret's value. Only the
// key is kept; the value never leaves FindLeaks.
type Leak struct {
	File string
	Line int
	Key  string
}

// MinLeakLen is the shortest value searched for; shorter ones ("true",
// "8080") would match everywhere.
const MinLeakLen = 8

// FindLeaks returns one Leak per line of content that contains the value of
// a secret. Multi-line values (private keys) match on any body line, PEM
// header and footer lines excluded.
func FindLeaks(secrets []Variable, file string, content []byte) []Leak {
	type needle struct {
		key string
		val []byte
	}
	var needles []needle
	for _, v := range secrets {
		if v.Kind != KindSecret {
			continue
		}
		for _, part := range strings.Split(v.Value, "\n") {
			part = strings.TrimSpace(part)
			if len(part) < MinLeakLen || strings.HasPrefix(part, "-----") {
				continue
			}
			needles = append(needles, needle{v.Key, []byte(part)})
		}
	}
	var out []Leak
	for i, line := range bytes.Split(content, []byte("\n")) {
		seen := map[string]bool{}
		for _, n := range needles {
			if !seen[n.key] && bytes.Contains(line, n.val) {
				seen[n.key] = true
				out = append(out, Leak{File: file, Line: i + 1, Key: n.key})
			}
		}
	}
	return out
}
