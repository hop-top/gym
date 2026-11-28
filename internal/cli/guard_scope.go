// Package cli — guard_scope.go: per-scope policy sets for the guard.
package cli

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/spf13/viper"

	"hop.top/gym/internal/eventbus"
	"hop.top/gym/internal/guard"
	"hop.top/gym/internal/scope"
)

// guardEngines caches the engines built for a process, keyed by the policy
// roots they were loaded from. A failed load is cached too, so a broken
// policy dir is read and reported once.
type guardEngines struct {
	mu sync.Mutex
	by map[string]*guardEngineEntry
}

type guardEngineEntry struct {
	eng    *guard.Engine
	err    error
	warned bool
}

// policyRoots are the folder scopes whose .gym/policies gate an operation
// on the scope rooted at root, outermost first: the chain of the command's
// start folder (the set every run has always used) plus the chain of root.
// The union is never smaller than the command's own set. Chain applies the
// ancestor trust check, so an untrusted ancestor contributes nothing. An
// empty root is the command's chain alone (global scope, or no scope).
func (a *App) policyRoots(root string) []string {
	seen := map[string]bool{}
	var roots []string
	add := func(rs []string) {
		for _, r := range rs {
			if !seen[r] {
				seen[r] = true
				roots = append(roots, r)
			}
		}
	}
	add(scope.Chain(a.startDir()).Roots)
	if root != "" {
		add(scope.Chain(root).Roots)
	}
	// Every root is an ancestor of one folder, so depth orders them.
	sort.SliceStable(roots, func(i, j int) bool { return len(roots[i]) < len(roots[j]) })
	return roots
}

// scopeEngine is the engine for the scope rooted at root, built on first
// use and reused for the rest of the process. The loading rules are
// buildGuardEngine's alone.
func (a *App) scopeEngine(root string) (*guard.Engine, error) {
	return a.scopeEntry(root).result()
}

func (a *App) scopeEntry(root string) *guardEngineEntry {
	roots := a.policyRoots(root)
	key := strings.Join(roots, "\x00")
	a.engines.mu.Lock()
	defer a.engines.mu.Unlock()
	if e, ok := a.engines.by[key]; ok {
		return e
	}
	e := &guardEngineEntry{}
	e.eng, e.err = a.buildGuardEngine(roots)
	if a.engines.by == nil {
		a.engines.by = map[string]*guardEngineEntry{}
	}
	a.engines.by[key] = e
	return e
}

func (e *guardEngineEntry) result() (*guard.Engine, error) { return e.eng, e.err }

// scopeGate decides whether an operation on the scope rooted at root can be
// policy-gated. A guard that never started is gateUnavailable's. A scope
// whose own policies do not load is "guard unavailable" for that scope
// alone: denied under --strict, otherwise warned once and not gated, as
// for a guard that failed to start. skip means do not publish the request.
func (a *App) scopeGate(root string) (skip bool, err error) {
	if err := a.gateUnavailable(); err != nil {
		return false, err
	}
	if root == "" || a.guardSub == nil {
		return false, nil
	}
	e := a.scopeEntry(root)
	if e.err == nil {
		return false, nil
	}
	if eventbus.IsStrict(viper.GetViper()) {
		return false, fmt.Errorf("%w: guard unavailable for scope %s: %v", guard.ErrPolicyDeny, root, e.err)
	}
	a.engines.mu.Lock()
	first := !e.warned
	e.warned = true
	a.engines.mu.Unlock()
	if first {
		a.Logger().Warnf("guard unavailable for scope %s; its operations are not policy-gated: %v", root, e.err)
	}
	return true, nil
}
