package config

import (
	"reflect"
	"testing"

	"github.com/bakhod1r/env4ci/internal/domain"
)

func TestAuthPath(t *testing.T) {
	if (Config{}).AuthPath() != DefaultAuthFile || (Config{AuthFile: "x"}).AuthPath() != "x" {
		t.Fatal("AuthPath")
	}
}

func TestTargetNames(t *testing.T) {
	if (Targets{}).Names() != nil {
		t.Fatal("empty")
	}
	got := Targets{GitHub: &GitHub{}, GitLab: &GitLab{}, Vault: &Vault{}}.Names()
	if !reflect.DeepEqual(got, []string{"github", "gitlab", "vault"}) {
		t.Fatalf("%v", got)
	}
}

func TestLoadReadError(t *testing.T) {
	if _, err := Load(t.TempDir()); err == nil { // a directory cannot be read as a file
		t.Fatal("want error")
	}
}

func TestClassifierDefaultsAndBadDefault(t *testing.T) {
	c, err := Config{}.Classifier()
	if err != nil || c.Classify("APP_X") != domain.KindVariable {
		t.Fatalf("defaults: %v", err)
	}
	if _, err := (Config{Default: "nope"}).Classifier(); err == nil {
		t.Fatal("want error")
	}
	c, err = Config{Default: "variable"}.Classifier()
	if err != nil || c.Classify("ANY") != domain.KindVariable {
		t.Fatalf("default variable: %v", err)
	}
}
