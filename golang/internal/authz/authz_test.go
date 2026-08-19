package authz

import "testing"

func TestAuthorizer_Allowed_ExactRepoGrant(t *testing.T) {
	// Arrange
	authorizer := NewAuthorizer([]Role{
		{Name: "reader", Grants: []Grant{{Repo: "public-bucket", Actions: []Action{ActionRead}}}},
	})
	p := Principal{ID: "alice", Roles: []string{"reader"}}

	// Act / Assert
	if !authorizer.Allowed(p, ActionRead, "public-bucket") {
		t.Error("Allowed(read, public-bucket) = false, want true")
	}
	if authorizer.Allowed(p, ActionRead, "other-bucket") {
		t.Error("Allowed(read, other-bucket) = true, want false (grant is scoped to public-bucket)")
	}
	if authorizer.Allowed(p, ActionWrite, "public-bucket") {
		t.Error("Allowed(write, public-bucket) = true, want false (role only grants read)")
	}
}

func TestAuthorizer_Allowed_WildcardRepoGrant(t *testing.T) {
	// Arrange
	authorizer := NewAuthorizer([]Role{
		{Name: "global-reader", Grants: []Grant{{Repo: "*", Actions: []Action{ActionRead}}}},
	})
	p := Principal{ID: "alice", Roles: []string{"global-reader"}}

	// Act / Assert
	for _, repo := range []string{"public-bucket", "private-bucket", "anything"} {
		if !authorizer.Allowed(p, ActionRead, repo) {
			t.Errorf("Allowed(read, %q) = false, want true (wildcard grant)", repo)
		}
	}
}

func TestAuthorizer_Allowed_DeniesByDefault(t *testing.T) {
	cases := []struct {
		name       string
		authorizer *Authorizer
		p          Principal
	}{
		{"no roles at all", NewAuthorizer(nil), Principal{ID: "alice"}},
		{"principal has no roles", NewAuthorizer([]Role{{Name: "reader", Grants: []Grant{{Repo: "*", Actions: []Action{ActionRead}}}}}), Principal{ID: "alice"}},
		{
			"principal's role name isn't registered",
			NewAuthorizer([]Role{{Name: "reader", Grants: []Grant{{Repo: "*", Actions: []Action{ActionRead}}}}}),
			Principal{ID: "alice", Roles: []string{"nonexistent-role"}},
		},
		{
			"role has no grant for this action",
			NewAuthorizer([]Role{{Name: "reader", Grants: []Grant{{Repo: "*", Actions: []Action{ActionRead}}}}}),
			Principal{ID: "alice", Roles: []string{"reader"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got := tc.authorizer.Allowed(tc.p, ActionAdmin, "any-bucket")

			// Assert
			if got {
				t.Error("Allowed() = true, want false (deny by default)")
			}
		})
	}
}

func TestAuthorizer_Allowed_UnionsGrantsAcrossMultipleRoles(t *testing.T) {
	// Arrange
	authorizer := NewAuthorizer([]Role{
		{Name: "reader", Grants: []Grant{{Repo: "bucket-a", Actions: []Action{ActionRead}}}},
		{Name: "relocator", Grants: []Grant{{Repo: "bucket-a", Actions: []Action{ActionRelocate}}}},
	})
	p := Principal{ID: "alice", Roles: []string{"reader", "relocator"}}

	// Act / Assert
	if !authorizer.Allowed(p, ActionRead, "bucket-a") {
		t.Error("Allowed(read) = false, want true (from the reader role)")
	}
	if !authorizer.Allowed(p, ActionRelocate, "bucket-a") {
		t.Error("Allowed(relocate) = false, want true (from the relocator role)")
	}
	if authorizer.Allowed(p, ActionAdmin, "bucket-a") {
		t.Error("Allowed(admin) = true, want false (neither role grants it)")
	}
}

func TestAuthorizer_Allowed_UnknownRoleNameIsIgnoredNotAnError(t *testing.T) {
	// Arrange
	authorizer := NewAuthorizer([]Role{
		{Name: "reader", Grants: []Grant{{Repo: "*", Actions: []Action{ActionRead}}}},
	})
	p := Principal{ID: "alice", Roles: []string{"nonexistent-role", "reader"}}

	// Act / Assert: the unknown role name doesn't block evaluating the
	// principal's other, valid roles.
	if !authorizer.Allowed(p, ActionRead, "any-bucket") {
		t.Error("Allowed(read) = false, want true (the valid \"reader\" role still applies)")
	}
}

func TestNewAuthorizer_DeepCopiesGrantsAndActions(t *testing.T) {
	// Arrange: regression test — mutating the []Role slice, a Role's
	// Grants, or a Grant's Actions in place *after* construction, through
	// the caller's original references, must never change an
	// already-built Authorizer's decisions.
	actions := []Action{ActionRead}
	grants := []Grant{{Repo: "*", Actions: actions}}
	roles := []Role{{Name: "reader", Grants: grants}}
	authorizer := NewAuthorizer(roles)
	p := Principal{ID: "alice", Roles: []string{"reader"}}

	if !authorizer.Allowed(p, ActionRead, "any-bucket") {
		t.Fatal("Allowed(read) = false before any mutation, want true")
	}

	// Act: mutate every level in place through the original references.
	actions[0] = ActionAdmin
	grants[0].Repo = "some-other-bucket"
	roles[0].Name = "renamed"

	// Assert: the Authorizer's decisions are unaffected by any of it.
	if !authorizer.Allowed(p, ActionRead, "any-bucket") {
		t.Error("Allowed(read, any-bucket) = false after mutating the caller's original slices, want true (unaffected)")
	}
	if authorizer.Allowed(p, ActionAdmin, "any-bucket") {
		t.Error("Allowed(admin, any-bucket) = true after mutating actions[0] in place, want false (Authorizer holds its own copy)")
	}
}

func TestAuthorizer_Allowed_DuplicateRoleNameKeepsLastOne(t *testing.T) {
	// Arrange
	authorizer := NewAuthorizer([]Role{
		{Name: "reader", Grants: []Grant{{Repo: "*", Actions: []Action{ActionRead}}}},
		{Name: "reader", Grants: []Grant{{Repo: "*", Actions: []Action{ActionWrite}}}},
	})
	p := Principal{ID: "alice", Roles: []string{"reader"}}

	// Act / Assert
	if authorizer.Allowed(p, ActionRead, "any-bucket") {
		t.Error("Allowed(read) = true, want false (second \"reader\" definition replaced the first)")
	}
	if !authorizer.Allowed(p, ActionWrite, "any-bucket") {
		t.Error("Allowed(write) = false, want true (from the surviving \"reader\" definition)")
	}
}
