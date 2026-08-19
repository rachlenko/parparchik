package authz

import (
	"reflect"
	"testing"
)

func TestServiceAccounts_Principal(t *testing.T) {
	// Arrange: Principal has a []string field (Roles), so equality needs
	// reflect.DeepEqual rather than == (structs with slice fields aren't
	// comparable with ==).
	alice := Principal{ID: "alice", Roles: []string{"reader"}}
	accounts := NewServiceAccounts(map[string]Principal{"api-key-1": alice})

	// Act
	got, ok := accounts.Principal("api-key-1")

	// Assert
	if !ok || !reflect.DeepEqual(got, alice) {
		t.Errorf("Principal(\"api-key-1\") = %+v, %v, want %+v, true", got, ok, alice)
	}
}

func TestServiceAccounts_Principal_UnknownKey(t *testing.T) {
	// Arrange
	accounts := NewServiceAccounts(map[string]Principal{"api-key-1": {ID: "alice"}})

	// Act
	_, ok := accounts.Principal("never-configured-key")

	// Assert
	if ok {
		t.Error("Principal(\"never-configured-key\") ok = true, want false")
	}
}

func TestSuperuserRole_EachCallReturnsAnIndependentCopy(t *testing.T) {
	// Arrange: regression test — SuperuserRole must be a function
	// returning a fresh Role each time, not a shared mutable package-level
	// value, or one caller mutating its Grants/Actions in place would
	// corrupt every other holder of "the same" role.
	a := SuperuserRole()
	b := SuperuserRole()
	const sentinel = Action("mutated-by-test")

	// Act: mutate a's nested Actions slice in place
	a.Grants[0].Actions[0] = sentinel

	// Assert: b's own backing array is unaffected
	for _, action := range b.Grants[0].Actions {
		if action == sentinel {
			t.Fatal("b.Grants[0].Actions contains the sentinel written into a's copy, want independent backing arrays")
		}
	}
}

func TestServiceAccounts_DoesNotAliasTheInputMap(t *testing.T) {
	// Arrange
	input := map[string]Principal{"api-key-1": {ID: "alice"}}
	accounts := NewServiceAccounts(input)

	// Act: mutate the map after construction
	input["api-key-1"] = Principal{ID: "mutated"}
	input["api-key-2"] = Principal{ID: "injected-after-construction"}

	// Assert
	got, ok := accounts.Principal("api-key-1")
	if !ok || got.ID != "alice" {
		t.Errorf("Principal(\"api-key-1\") = %+v, want the original alice principal, unaffected by later mutation of the input map", got)
	}
	if _, ok := accounts.Principal("api-key-2"); ok {
		t.Error("Principal(\"api-key-2\") ok = true, want false (key added to the input map after construction)")
	}
}

func TestServiceAccounts_DoesNotAliasAPrincipalsRolesSlice(t *testing.T) {
	// Arrange: regression test — mutating a Principal's Roles slice in
	// place *after* construction, through the caller's original reference
	// (not by reassigning the map entry — that's already covered by
	// TestServiceAccounts_DoesNotAliasTheInputMap), must not affect the
	// stored Principal.
	roles := []string{"reader"}
	input := map[string]Principal{"api-key-1": {ID: "alice", Roles: roles}}
	accounts := NewServiceAccounts(input)

	// Act
	roles[0] = "superuser"

	// Assert
	got, ok := accounts.Principal("api-key-1")
	if !ok || len(got.Roles) != 1 || got.Roles[0] != "reader" {
		t.Errorf("Principal(\"api-key-1\").Roles = %v after mutating the caller's original Roles slice, want [reader] unaffected", got.Roles)
	}
}

func TestSuperuserRole_ReproducesTodaysAllOrNothingAuthConfigBehavior(t *testing.T) {
	// Arrange: this is the documented migration path for a deployment
	// that hasn't adopted per-key roles — map every existing API key to a
	// Principal holding only SuperuserRole().
	authorizer := NewAuthorizer([]Role{SuperuserRole()})
	p := Principal{ID: "legacy-key-owner", Roles: []string{SuperuserRoleName}}

	// Act / Assert: every action, against every repository, is allowed —
	// exactly AuthConfig's current all-or-nothing behavior for any
	// accepted key.
	for _, action := range []Action{ActionRead, ActionWrite, ActionRelocate, ActionAdmin} {
		for _, repo := range []string{"public-bucket", "private-bucket", "anything"} {
			if !authorizer.Allowed(p, action, repo) {
				t.Errorf("Allowed(%v, %q) = false, want true (SuperuserRole)", action, repo)
			}
		}
	}
}
