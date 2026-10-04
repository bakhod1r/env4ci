// Package application holds env4ci use cases. Providers are reached only
// through the Provider port, so use cases are testable with a fake.
package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/bakhod1r/env4ci/internal/domain"
)

// Provider is the port every CI/CD backend implements.
type Provider interface {
	Name() string
	List(ctx context.Context) ([]domain.Remote, error)
	Set(ctx context.Context, v domain.Variable) error
	Delete(ctx context.Context, key string, kind domain.Kind) error
}

// Validator is optionally implemented by providers that can reject a value
// before any write (size limits, masking rules). Plan calls it for every
// variable, so a push never stops halfway on a value the provider refuses.
type Validator interface {
	Validate(v domain.Variable) error
}

// Service runs plan/apply/pull against one provider target.
type Service struct {
	Provider Provider
}

// Plan compares local variables with the provider.
func (s Service) Plan(ctx context.Context, local []domain.Variable) (domain.Plan, error) {
	for _, v := range local {
		if err := domain.ValidateKey(v.Key); err != nil {
			return domain.Plan{}, err
		}
	}
	if val, ok := s.Provider.(Validator); ok {
		var errs []error
		for _, v := range local {
			if err := val.Validate(v); err != nil {
				errs = append(errs, err)
			}
		}
		if len(errs) > 0 {
			return domain.Plan{}, errors.Join(errs...)
		}
	}
	remote, err := s.Provider.List(ctx)
	if err != nil {
		return domain.Plan{}, fmt.Errorf("%s: list: %w", s.Provider.Name(), err)
	}
	return domain.ComputePlan(local, remote), nil
}

// ApplyOptions controls destructive behaviour.
type ApplyOptions struct {
	Prune bool // delete remote-only keys
}

// Result counts what Apply did.
type Result struct {
	Written, Deleted int
}

// Apply executes a plan. A key that changed kind is deleted from its old
// store after the new one is written, so it never exists in both.
func (s Service) Apply(ctx context.Context, local []domain.Variable, plan domain.Plan, remote []domain.Remote, opt ApplyOptions) (Result, error) {
	byKey := make(map[string]domain.Variable, len(local))
	for _, v := range local {
		byKey[v.Key] = v
	}
	oldKind := make(map[string]domain.Kind, len(remote))
	for _, r := range remote {
		oldKind[r.Key] = r.Kind
	}

	var res Result
	for _, c := range plan.Changes {
		switch c.Action {
		case domain.ActionCreate, domain.ActionUpdate, domain.ActionUnverifiable:
			v := byKey[c.Key]
			if err := s.Provider.Set(ctx, v); err != nil {
				return res, fmt.Errorf("set %s: %w", c.Key, err)
			}
			res.Written++
			if k, ok := oldKind[c.Key]; ok && k != v.Kind {
				if err := s.Provider.Delete(ctx, c.Key, k); err != nil {
					return res, fmt.Errorf("delete old %s %s: %w", k, c.Key, err)
				}
			}
		case domain.ActionRemoteOnly:
			if !opt.Prune {
				continue
			}
			if err := s.Provider.Delete(ctx, c.Key, c.Kind); err != nil {
				return res, fmt.Errorf("delete %s: %w", c.Key, err)
			}
			res.Deleted++
		}
	}
	return res, nil
}

// Pull returns remote variables whose values are readable, plus the keys
// whose values the provider hides.
func (s Service) Pull(ctx context.Context) (known []domain.Remote, hidden []string, err error) {
	remote, err := s.Provider.List(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, r := range remote {
		if r.Known {
			known = append(known, r)
		} else {
			hidden = append(hidden, r.Key)
		}
	}
	return known, hidden, nil
}
