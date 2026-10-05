// Package audit appends push records to a JSON Lines file.
package audit

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/bakhod1r/env4ci/internal/domain"
)

// Append writes e as one JSON line at the end of path, creating it (and its
// folder) if needed. Entries hold no values, so the file is not secret.
func Append(path string, e domain.AuditEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { // #nosec G301 -- audit log is committed, not secret
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644) // #nosec G302 G304 -- path from env4ci.yaml
	if err != nil {
		return err
	}
	line, _ := json.Marshal(e) // plain struct of strings: cannot fail
	_, err = f.Write(append(line, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
