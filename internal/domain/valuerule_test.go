package domain

import (
	"regexp"
	"strings"
	"testing"
)

func TestCheckValues(t *testing.T) {
	rules := map[string]ValueRule{
		"DATABASE_URL": {Required: true, Type: "url", Pattern: regexp.MustCompile(`^postgres://`)},
		"APP_PORT":     {Type: "port"},
		"WORKERS":      {Type: "int"},
		"DEBUG":        {Type: "bool"},
		"ADMIN_EMAIL":  {Type: "email"},
		"JWT_SECRET":   {MinLen: 32, MaxLen: 64},
		"LOG_LEVEL":    {OneOf: []string{"debug", "info"}},
		"SENTRY_DSN":   {RequiredIn: []string{"production"}},
		"OPTIONAL":     {Type: "int"}, // absent and not required: fine
	}
	bad := []Variable{
		{Key: "DATABASE_URL", Value: "mysql://secret-host/db"},
		{Key: "APP_PORT", Value: "70000"},
		{Key: "WORKERS", Value: "four"},
		{Key: "DEBUG", Value: "maybe"},
		{Key: "ADMIN_EMAIL", Value: "nobody"},
		{Key: "JWT_SECRET", Value: "short"},
		{Key: "LOG_LEVEL", Value: "trace"},
	}
	errs := CheckValues(bad, rules, "production")
	var msgs []string
	for _, e := range errs {
		msgs = append(msgs, e.Error())
	}
	got := strings.Join(msgs, "\n")
	want := strings.Join([]string{
		"ADMIN_EMAIL: not an email address",
		"APP_PORT: not a port (1-65535)",
		"DATABASE_URL: does not match ^postgres://",
		"DEBUG: not a bool (true/false/1/0/yes/no)",
		"JWT_SECRET: shorter than 32 characters",
		"LOG_LEVEL: not one of debug, info",
		"SENTRY_DSN: required in production but missing",
		"WORKERS: not an integer",
	}, "\n")
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if strings.Contains(got, "secret-host") || strings.Contains(got, "trace") {
		t.Fatal("value leaked into message")
	}

	good := []Variable{
		{Key: "DATABASE_URL", Value: "postgres://h/db"},
		{Key: "APP_PORT", Value: "8080"},
		{Key: "WORKERS", Value: "-4"},
		{Key: "DEBUG", Value: "Yes"},
		{Key: "ADMIN_EMAIL", Value: "a@b.io"},
		{Key: "JWT_SECRET", Value: strings.Repeat("x", 40)},
		{Key: "LOG_LEVEL", Value: "info"},
	}
	if errs := CheckValues(good, rules, "staging"); len(errs) != 0 {
		t.Fatalf("good: %v", errs)
	}
	// Empty counts as missing; length/type rules then do not pile on.
	errs = CheckValues([]Variable{{Key: "DATABASE_URL"}, {Key: "JWT_SECRET", Value: strings.Repeat("x", 65)}}, rules, "")
	if len(errs) != 2 || errs[0].Error() != "DATABASE_URL: required but missing" || errs[1].Error() != "JWT_SECRET: longer than 64 characters" {
		t.Fatalf("got %v", errs)
	}
	if errs := CheckValues([]Variable{{Key: "DATABASE_URL", Value: "postgres://"}}, rules, ""); len(errs) != 1 || errs[0].Error() != "DATABASE_URL: not a URL (scheme://host...)" {
		t.Fatalf("url without host: %v", errs)
	}
}

func TestValidValueType(t *testing.T) {
	for _, ty := range []string{"", "url", "port", "int", "bool", "email"} {
		if !ValidValueType(ty) {
			t.Fatalf("%q should be valid", ty)
		}
	}
	if ValidValueType("uuid") {
		t.Fatal("uuid is not supported")
	}
}
