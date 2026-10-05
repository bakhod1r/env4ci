package domain

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// ValueRule constrains one key's value. Messages never include the value.
type ValueRule struct {
	Required   bool     // must be present and non-empty in every file
	RequiredIn []string // must be present in these environments
	Type       string   // url | port | int | bool | email
	Pattern    *regexp.Regexp
	MinLen     int
	MaxLen     int
	OneOf      []string
}

var valueTypes = map[string]func(string) bool{
	"url": func(v string) bool {
		u, err := url.Parse(v)
		return err == nil && u.Scheme != "" && u.Host != ""
	},
	"port": func(v string) bool {
		n, err := strconv.Atoi(v)
		return err == nil && n >= 1 && n <= 65535
	},
	"int": func(v string) bool {
		_, err := strconv.ParseInt(v, 10, 64)
		return err == nil
	},
	"bool": func(v string) bool {
		switch strings.ToLower(v) {
		case "true", "false", "1", "0", "yes", "no":
			return true
		}
		return false
	},
	"email": func(v string) bool {
		at := strings.LastIndex(v, "@")
		return at > 0 && strings.Contains(v[at+1:], ".") && !strings.ContainsAny(v, " \t")
	},
}

var typeHelp = map[string]string{
	"url":   "not a URL (scheme://host...)",
	"port":  "not a port (1-65535)",
	"int":   "not an integer",
	"bool":  "not a bool (true/false/1/0/yes/no)",
	"email": "not an email address",
}

// ValidValueType reports whether t is a supported rule type ("" = none).
func ValidValueType(t string) bool {
	_, ok := valueTypes[t]
	return t == "" || ok
}

// CheckValues applies rules to vars for environment env ("" = shared) and
// returns at most one error per key, sorted by key.
func CheckValues(vars []Variable, rules map[string]ValueRule, env string) []error {
	values := make(map[string]string, len(vars))
	for _, v := range vars {
		values[v.Key] = v.Value
	}
	keys := make([]string, 0, len(rules))
	for k := range rules {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var errs []error
	for _, k := range keys {
		if msg := checkValue(rules[k], values[k], env); msg != "" {
			errs = append(errs, fmt.Errorf("%s: %s", k, msg))
		}
	}
	return errs
}

func checkValue(r ValueRule, v, env string) string {
	if v == "" {
		switch {
		case r.Required:
			return "required but missing"
		case env != "" && slices.Contains(r.RequiredIn, env):
			return "required in " + env + " but missing"
		}
		return ""
	}
	switch {
	case r.Type != "" && !valueTypes[r.Type](v):
		return typeHelp[r.Type]
	case r.MinLen > 0 && len(v) < r.MinLen:
		return fmt.Sprintf("shorter than %d characters", r.MinLen)
	case r.MaxLen > 0 && len(v) > r.MaxLen:
		return fmt.Sprintf("longer than %d characters", r.MaxLen)
	case len(r.OneOf) > 0 && !slices.Contains(r.OneOf, v):
		return "not one of " + strings.Join(r.OneOf, ", ")
	case r.Pattern != nil && !r.Pattern.MatchString(v):
		return "does not match " + r.Pattern.String()
	}
	return ""
}
