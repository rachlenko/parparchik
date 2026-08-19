// Package authz models per-repository, per-action authorization —
// role-based access control on top of, not instead of, the existing
// flat-API-key authentication in internal/httpapi.AuthConfig.
//
// The two packages have deliberately different jobs: AuthConfig answers
// "is this a valid, currently-configured API key at all" (authentication —
// unchanged, still the only thing wired into the live request path).
// Authorizer answers a question AuthConfig cannot: "is this specific,
// already-authenticated caller allowed to do this specific thing to this
// specific repository" (authorization). ServiceAccounts is the bridge
// between them: it maps an API key AuthConfig has already validated to the
// Principal it should act as under the new role model — per the plan's
// explicit instruction, this keeps every existing flat-API-key deployment
// working as a "service account" identity tier, not a config format this
// package replaces.
//
// Implemented: Action, Grant, Role, Principal, Authorizer.Allowed
// (deny-by-default, union of every role's grants), and ServiceAccounts
// (API key -> Principal).
//
// Not implemented (see docs/plans/ Task 32): wiring Authorizer into
// httpapi's live request path (AuthConfig's all-or-nothing gate is
// unchanged), and OIDC/SSO integration for a future UI — that's an
// authentication mechanism, a different concern from this package, and
// needs a concrete identity provider to integrate against, which this
// project doesn't have. A Principal here is agnostic to how it was
// authenticated (API key today, an OIDC token later); wiring in a second
// authentication method only ever needs to produce a Principal, never
// touch this package's authorization logic.
package authz

import "log/slog"

// Action is one of the operations this service's routes perform against a
// repository. Deliberately a small, closed set matching what actually
// exists or is explicitly planned, not an open string a caller could
// typo — see the httpapi route each maps to in its own doc comment.
type Action string

const (
	// ActionRead covers GET /{bucket}/{key} (download), GET /list, and
	// GET /update (a read-only lookup, even though its name says
	// "update" — it never mutates).
	ActionRead Action = "read"

	// ActionWrite covers uploading a new object into a repository. No
	// httpapi route does this yet (objects arrive in S3 externally,
	// same limitation noted throughout internal/scan, internal/license,
	// internal/sbom); this exists so a future upload endpoint has a
	// permission to check from day one instead of retrofitting one.
	ActionWrite Action = "write"

	// ActionRelocate covers POST /relocate.
	ActionRelocate Action = "relocate"

	// ActionAdmin covers repository-management operations that aren't
	// per-object at all — e.g. a future endpoint to change a
	// repository's cleanup/retention rule (internal/cleanup) or policy
	// ruleset (internal/policy). No httpapi route does this yet either.
	ActionAdmin Action = "admin"
)

// wildcardRepo grants a Role's Actions against every repository, not one
// specific bucket by name.
const wildcardRepo = "*"

// Grant authorizes Actions against Repo, an exact bucket name or
// wildcardRepo ("*") for every repository. There is no glob/prefix
// matching beyond the single wildcard value — a deliberate scope cut, not
// an oversight; add real pattern matching only once a concrete deployment
// needs more than "this one bucket" or "every bucket".
type Grant struct {
	Repo    string
	Actions []Action
}

func (g Grant) matches(repo string, action Action) bool {
	if g.Repo != wildcardRepo && g.Repo != repo {
		return false
	}
	for _, a := range g.Actions {
		if a == action {
			return true
		}
	}
	return false
}

// Role is a named, reusable bundle of Grants.
type Role struct {
	Name   string
	Grants []Grant
}

// Principal is an already-authenticated caller: an identity (ID is
// whatever the caller wants for logging/audit — an API key's owner name,
// an OIDC subject, anything) plus the Role names it holds. A Roles entry
// that doesn't match any Role known to the Authorizer it's checked
// against is silently ignored, not an error — see Authorizer.Allowed.
type Principal struct {
	ID    string
	Roles []string
}

// Authorizer holds the full set of Roles a deployment has defined, keyed
// by name, and decides whether a Principal may perform an Action against
// a repository.
type Authorizer struct {
	roles map[string]Role
}

// NewAuthorizer builds an Authorizer from roles. Every Role's Grants, and
// every Grant's Actions, are deep-copied into freshly allocated slices —
// not just the top-level []Role — so a caller that mutates a Grant's
// Actions slice (or a Role's Grants slice) in place after construction,
// through whatever reference they originally built it with, can never
// change an already-built Authorizer's decisions. This matters
// specifically because Allowed will eventually run concurrently with
// arbitrary other code once this package is wired into request handling
// (see the package doc comment) — an Authorizer must own independent data,
// not share backing arrays with anything outside it.
//
// A duplicate Role.Name logs a warning and keeps the last one — callers
// constructing roles programmatically control uniqueness themselves; this
// package doesn't otherwise validate config shape (no config-loading code
// exists here to validate yet — see the package doc comment).
func NewAuthorizer(roles []Role) *Authorizer {
	byName := make(map[string]Role, len(roles))
	for _, r := range roles {
		if _, dup := byName[r.Name]; dup {
			slog.Warn("authz: duplicate role name, last definition wins", "role", r.Name)
		}
		byName[r.Name] = cloneRole(r)
	}
	return &Authorizer{roles: byName}
}

func cloneRole(r Role) Role {
	grants := make([]Grant, len(r.Grants))
	for i, g := range r.Grants {
		grants[i] = Grant{Repo: g.Repo, Actions: append([]Action(nil), g.Actions...)}
	}
	return Role{Name: r.Name, Grants: grants}
}

// Allowed reports whether p may perform action against repo. Deny by
// default: a Principal with no roles, a Role name not found in a, or no
// Grant among all of p's (found) roles matching both repo and action all
// return false — there is no implicit "authenticated therefore allowed"
// fallback anywhere in this decision.
func (a *Authorizer) Allowed(p Principal, action Action, repo string) bool {
	for _, roleName := range p.Roles {
		role, ok := a.roles[roleName]
		if !ok {
			continue
		}
		for _, grant := range role.Grants {
			if grant.matches(repo, action) {
				return true
			}
		}
	}
	return false
}
