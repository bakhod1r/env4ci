package domain

import (
	"reflect"
	"testing"
)

func TestFindLeaks(t *testing.T) {
	key := "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAABG5vbmU=\n-----END OPENSSH PRIVATE KEY-----"
	secrets := []Variable{
		{Key: "API_TOKEN", Value: "tok_live_1234567", Kind: KindSecret},
		{Key: "SHORT", Value: "abc", Kind: KindSecret},                 // too short to match safely
		{Key: "LOG_LEVEL", Value: "debug-verbose", Kind: KindVariable}, // not a secret
		{Key: "SSH_PRIVATE_KEY", Value: key, Kind: KindSecret},
	}
	content := []byte("a: 1\ntoken = \"tok_live_1234567\" # tok_live_1234567\nabc debug-verbose\n  b3BlbnNzaC1rZXktdjEAAAAABG5vbmU=\n")
	got := FindLeaks(secrets, "cfg.yml", content)
	want := []Leak{
		{File: "cfg.yml", Line: 2, Key: "API_TOKEN"},
		{File: "cfg.yml", Line: 4, Key: "SSH_PRIVATE_KEY"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
	if got := FindLeaks(secrets, "x", []byte("clean\n")); got != nil {
		t.Fatalf("clean: %+v", got)
	}
}
