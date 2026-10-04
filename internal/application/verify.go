package application

import (
	"context"
	"sync"
)

// Check verifies one credential (SSH login, registry login) before it is
// written to CI. Keys are the variables it depends on; Run must never put a
// value into its error.
type Check struct {
	Name string
	Keys []string
	Run  func(ctx context.Context) error
}

type CheckResult struct {
	Name string
	Keys []string
	Err  error
}

// RunChecks runs, in parallel, every check that depends on at least one key
// in touched (nil = all). Results keep the input order.
func RunChecks(ctx context.Context, checks []Check, touched map[string]bool) []CheckResult {
	var selected []Check
	for _, c := range checks {
		if touched == nil || anyKey(c.Keys, touched) {
			selected = append(selected, c)
		}
	}
	results := make([]CheckResult, len(selected))
	var wg sync.WaitGroup
	for i, c := range selected {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = CheckResult{Name: c.Name, Keys: c.Keys, Err: c.Run(ctx)}
		}()
	}
	wg.Wait()
	return results
}

// AnyFailed reports whether a check failed.
func AnyFailed(rs []CheckResult) bool {
	for _, r := range rs {
		if r.Err != nil {
			return true
		}
	}
	return false
}

// TouchedKeys lists keys a plan would write.
func TouchedKeys(p interface{ WriteKeys() []string }) map[string]bool {
	m := map[string]bool{}
	for _, k := range p.WriteKeys() {
		m[k] = true
	}
	return m
}

func anyKey(keys []string, set map[string]bool) bool {
	for _, k := range keys {
		if set[k] {
			return true
		}
	}
	return false
}
