package authz

// ServiceAccounts maps an API key — one httpapi.AuthConfig has already
// validated as one of its configured keys — to the Principal that key
// acts as. This is the "service account" identity tier the plan requires
// stay working alongside the new role model: a deployment that hasn't
// adopted per-key roles yet, and doesn't want to, can map every existing
// key to a Principal holding a single wildcard-repo, all-Actions Role —
// see SuperuserRole below — reproducing today's all-or-nothing behavior
// exactly under the new model, not as a special case this package treats
// differently.
type ServiceAccounts struct {
	byKey map[string]Principal
}

// NewServiceAccounts builds a ServiceAccounts from a key->Principal map.
// Every Principal's Roles slice is deep-copied, not just the top-level
// map, so a caller mutating either the map or a Principal's Roles slice
// in place afterward — through whatever reference they originally built
// it with — cannot affect this ServiceAccounts.
func NewServiceAccounts(principals map[string]Principal) *ServiceAccounts {
	byKey := make(map[string]Principal, len(principals))
	for k, p := range principals {
		byKey[k] = Principal{ID: p.ID, Roles: append([]string(nil), p.Roles...)}
	}
	return &ServiceAccounts{byKey: byKey}
}

// Principal returns the Principal apiKey maps to. ok is false for a key
// with no configured mapping — distinct from, and checked after,
// AuthConfig's own key-validity check: a key AuthConfig accepts but that
// has no entry here has authenticated successfully but has no
// authorization Principal to evaluate, which a caller should treat as
// "deny", not "fall back to some default access level".
func (s *ServiceAccounts) Principal(apiKey string) (Principal, bool) {
	p, ok := s.byKey[apiKey]
	return p, ok
}

// SuperuserRoleName is the Name of the Role SuperuserRole returns —
// exported so a Principal can reference it in Roles without importing a
// magic string, e.g. Principal{Roles: []string{authz.SuperuserRoleName}}.
const SuperuserRoleName = "superuser"

// SuperuserRole returns a Role granting every Action against every
// repository — the exact permission shape today's AuthConfig gives any
// accepted API key (a valid key can reach every route, unconditionally).
// Mapping every existing key to a Principal holding only this Role is how
// a deployment reproduces current behavior precisely under the new role
// model, without this package needing a separate "legacy mode" code path.
//
// This is a function, not a package-level var, deliberately: a shared
// mutable Role value would let one caller's in-place mutation of its
// Grants/Actions slices (a natural-looking "start from SuperuserRole and
// narrow it" pattern) corrupt every other holder of the same value,
// including concurrently-running Authorizer.Allowed calls once this
// package is wired into request handling. Every call returns its own,
// independent Role — NewAuthorizer additionally deep-copies it anyway
// (see NewAuthorizer's doc comment), so this is defense in depth, not the
// only thing preventing that class of bug.
func SuperuserRole() Role {
	return Role{
		Name: SuperuserRoleName,
		Grants: []Grant{
			{Repo: wildcardRepo, Actions: []Action{ActionRead, ActionWrite, ActionRelocate, ActionAdmin}},
		},
	}
}
