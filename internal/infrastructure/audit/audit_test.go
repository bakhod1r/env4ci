package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakhod1r/env4ci/internal/domain"
)

func TestAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "audit.jsonl")
	e := domain.AuditEntry{Time: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Actor: "me", Provider: "github", Target: "o/r",
		Changes: []domain.AuditChange{{Key: "A", Kind: "secret", Action: "create"}}, Result: "ok"}
	if err := Append(path, e); err != nil {
		t.Fatal(err)
	}
	if err := Append(path, e); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %q", b)
	}
	var got domain.AuditEntry
	if err := json.Unmarshal([]byte(lines[1]), &got); err != nil || got.Actor != "me" || got.Changes[0].Key != "A" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if !strings.HasPrefix(lines[0], `{"time":"2026-01-02T03:04:05Z"`) {
		t.Fatalf("line = %s", lines[0])
	}
}

func TestAppendErrors(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "f"), nil, 0o644)
	if err := Append(filepath.Join(dir, "f", "x.jsonl"), domain.AuditEntry{}); err == nil {
		t.Fatal("folder blocked: want error")
	}
	if err := Append(dir, domain.AuditEntry{}); err == nil {
		t.Fatal("path is a folder: want error")
	}
}
