package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteMakefile(t *testing.T) {
	dir := t.TempDir()
	mk := filepath.Join(dir, "Makefile")
	var out bytes.Buffer

	// Created from nothing.
	if err := writeMakefile(mk, &out); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(mk)
	golden(t, "Makefile", string(b))
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "    ") {
			t.Fatalf("recipe indented with spaces, make needs tabs: %q", line)
		}
	}

	// Rerun: up to date.
	out.Reset()
	writeMakefile(mk, &out)
	if !strings.Contains(out.String(), "up to date") {
		t.Fatal(out.String())
	}

	// Existing Makefile without trailing newline: block appended, own targets kept.
	os.WriteFile(mk, []byte("build:\n\tgo build ./..."), 0o644)
	writeMakefile(mk, io.Discard)
	b, _ = os.ReadFile(mk)
	if !strings.HasPrefix(string(b), "build:\n\tgo build ./...\n\n"+makeBegin) {
		t.Fatalf("%s", b)
	}

	// Stale block between markers is replaced, text after it kept.
	os.WriteFile(mk, []byte("a:\n\ttrue\n"+makeBegin+"\nold\n"+makeEnd+"\nz:\n\ttrue\n"), 0o644)
	out.Reset()
	writeMakefile(mk, &out)
	b, _ = os.ReadFile(mk)
	if strings.Contains(string(b), "\nold\n") || !strings.HasSuffix(string(b), makeEnd+"\nz:\n\ttrue\n") || !strings.Contains(out.String(), "refreshed") {
		t.Fatalf("%s\n%s", b, out.String())
	}

	// Broken markers: refuse.
	os.WriteFile(mk, []byte(makeBegin+"\n"), 0o644)
	if err := writeMakefile(mk, io.Discard); err == nil || !strings.Contains(err.Error(), "unbalanced") {
		t.Fatalf("err = %v", err)
	}
	// Unreadable path (a directory).
	if err := writeMakefile(dir, io.Discard); err == nil {
		t.Fatal("want error")
	}
}

func TestMakefileCommandAndInit(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "env4ci.yaml")
	os.WriteFile(cfg, []byte("source: .env\n"), 0o644)
	if err := run(context.Background(), []string{"makefile", "-c", cfg}, nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "Makefile")); !strings.Contains(string(b), "env-push:") {
		t.Fatalf("%s", b)
	}

	dir2 := t.TempDir()
	o := opts{config: filepath.Join(dir2, "env4ci.yaml"), targets: "vault", envs: "production"}
	if err := cmdInitWizard(o, nil, io.Discard, fakeGit{remote: ghRemote}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir2, "Makefile")); err != nil {
		t.Fatal("init did not write Makefile")
	}
}
