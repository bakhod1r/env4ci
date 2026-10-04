package main

import (
	"io"
	"os"
)

// palette colors terminal output. Disabled for pipes/files, NO_COLOR, TERM=dumb;
// FORCE_COLOR enables it anyway.
type palette struct{ on bool }

func newPalette(w io.Writer) palette {
	if os.Getenv("FORCE_COLOR") != "" {
		return palette{on: true}
	}
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return palette{}
	}
	f, ok := w.(*os.File)
	if !ok {
		return palette{}
	}
	st, err := f.Stat()
	return palette{on: err == nil && st.Mode()&os.ModeCharDevice != 0}
}

func (p palette) wrap(code, s string) string {
	if !p.on {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (p palette) Green(s string) string  { return p.wrap("32", s) }
func (p palette) Yellow(s string) string { return p.wrap("33", s) }
func (p palette) Red(s string) string    { return p.wrap("31", s) }
func (p palette) Purple(s string) string { return p.wrap("35", s) }
func (p palette) Dim(s string) string    { return p.wrap("2", s) }
func (p palette) Bold(s string) string   { return p.wrap("1", s) }
