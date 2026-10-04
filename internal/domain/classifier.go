package domain

import (
	"path"
	"strings"
)

// Rule maps a glob pattern (e.g. "*SECRET*") to a Kind.
type Rule struct {
	Pattern string
	Kind    Kind
}

// Classifier decides the Kind of a key. Rules are checked in order; the first
// match wins. Keys matching no rule get the fallback Kind.
type Classifier struct {
	rules    []Rule
	fallback Kind
}

func NewClassifier(rules []Rule, fallback Kind) Classifier {
	return Classifier{rules: rules, fallback: fallback}
}

// DefaultClassifier treats anything unknown as a secret: leaking a value
// as a plain variable is worse than hiding a harmless one.
func DefaultClassifier() Classifier {
	return NewClassifier([]Rule{
		{"*SECRET*", KindSecret},
		{"*PASSWORD*", KindSecret},
		{"*TOKEN*", KindSecret},
		{"*KEY*", KindSecret},
		{"*_URL", KindSecret},
		{"*_DSN", KindSecret},
		{"APP_*", KindVariable},
		{"LOG_*", KindVariable},
	}, KindSecret)
}

func (c Classifier) Classify(key string) Kind {
	upper := strings.ToUpper(key)
	for _, r := range c.rules {
		if ok, _ := path.Match(strings.ToUpper(r.Pattern), upper); ok {
			return r.Kind
		}
	}
	return c.fallback
}
