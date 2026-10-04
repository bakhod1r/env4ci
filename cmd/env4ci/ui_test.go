package main

import (
	"bytes"
	"testing"
)

func TestPalette(t *testing.T) {
	t.Setenv("FORCE_COLOR", "")
	t.Setenv("NO_COLOR", "")
	if p := newPalette(&bytes.Buffer{}); p.on || p.Red("x") != "x" {
		t.Fatal("buffer must be plain")
	}
	t.Setenv("FORCE_COLOR", "1")
	if p := newPalette(&bytes.Buffer{}); p.Red("x") != "\x1b[31mx\x1b[0m" {
		t.Fatal("FORCE_COLOR ignored")
	}
}
