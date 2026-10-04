package domain

import "testing"

func TestClassifierFirstMatchWins(t *testing.T) {
	c := NewClassifier([]Rule{
		{Pattern: "APP_SECRET_*", Kind: KindVariable},
		{Pattern: "*SECRET*", Kind: KindSecret},
	}, KindSecret)

	cases := map[string]Kind{
		"APP_SECRET_NAME": KindVariable,
		"JWT_SECRET":      KindSecret,
		"UNKNOWN":         KindSecret, // fallback
	}
	for key, want := range cases {
		if got := c.Classify(key); got != want {
			t.Errorf("Classify(%q) = %v, want %v", key, got, want)
		}
	}
}

func TestDefaultClassifier(t *testing.T) {
	c := DefaultClassifier()
	for key, want := range map[string]Kind{
		"DATABASE_PASSWORD":  KindSecret,
		"TELEGRAM_BOT_TOKEN": KindSecret,
		"API_KEY":            KindSecret,
		"DATABASE_URL":       KindSecret,
		"APP_PORT":           KindVariable,
		"LOG_LEVEL":          KindVariable,
		"REDIS_HOST":         KindSecret, // unknown keys are secrets: safe default
	} {
		if got := c.Classify(key); got != want {
			t.Errorf("Classify(%q) = %v, want %v", key, got, want)
		}
	}
}

func TestComputePlan(t *testing.T) {
	local := []Variable{
		{Key: "A", Value: "1", Kind: KindVariable},
		{Key: "B", Value: "2", Kind: KindVariable},
		{Key: "S", Value: "x", Kind: KindSecret},
	}
	remote := []Remote{
		{Key: "A", Kind: KindVariable, Value: "1", Known: true},
		{Key: "B", Kind: KindVariable, Value: "old", Known: true},
		{Key: "S", Kind: KindSecret, Known: false},
		{Key: "OLD", Kind: KindVariable, Value: "z", Known: true},
	}
	p := ComputePlan(local, remote)

	want := map[string]Action{"A": ActionNoop, "B": ActionUpdate, "S": ActionUnverifiable, "OLD": ActionRemoteOnly}
	if len(p.Changes) != len(want) {
		t.Fatalf("got %d changes, want %d", len(p.Changes), len(want))
	}
	for _, ch := range p.Changes {
		if want[ch.Key] != ch.Action {
			t.Errorf("%s: got %v, want %v", ch.Key, ch.Action, want[ch.Key])
		}
	}
	if p.Changes[0].Key != "A" || p.Changes[3].Key != "S" {
		t.Errorf("changes not sorted: %+v", p.Changes)
	}
}

func TestComputePlanKindChangeIsUpdate(t *testing.T) {
	p := ComputePlan(
		[]Variable{{Key: "A", Value: "1", Kind: KindSecret}},
		[]Remote{{Key: "A", Value: "1", Kind: KindVariable, Known: true}},
	)
	if p.Changes[0].Action != ActionUpdate {
		t.Fatalf("got %v", p.Changes[0].Action)
	}
}

func TestComputePlanCreate(t *testing.T) {
	p := ComputePlan([]Variable{{Key: "N", Value: "v", Kind: KindVariable}}, nil)
	if p.Changes[0].Action != ActionCreate || !p.HasWrites() {
		t.Fatalf("got %+v", p)
	}
}

func TestValidKey(t *testing.T) {
	for k, ok := range map[string]bool{"A_1": true, "_X": true, "1A": false, "A-B": false, "": false, "GITHUB_TOKEN": false} {
		if err := ValidateKey(k); (err == nil) != ok {
			t.Errorf("ValidateKey(%q) err=%v, want ok=%v", k, err, ok)
		}
	}
}
