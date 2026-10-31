package runtime

import (
	"encoding/base64"
	"encoding/binary"
	"slices"
	"testing"

	"github.com/admirable-oss/hive/internal/access"
	"github.com/admirable-oss/hive/internal/presence"
)

func testKey(t *testing.T, seed byte) access.PublicKey {
	t.Helper()
	typ := "ssh-ed25519"
	blob := binary.BigEndian.AppendUint32(nil, uint32(len(typ)))
	blob = append(blob, typ...)
	blob = binary.BigEndian.AppendUint32(blob, 32)
	for i := range 32 {
		blob = append(blob, seed+byte(i))
	}
	k, err := access.ParseKey(typ + " " + base64.StdEncoding.EncodeToString(blob))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// TestNotificationRecipients: work's owner is told while they are here and
// may see it; otherwise the fallback is, and the runtime's own user when
// the fallback names nobody who may see it.
func TestNotificationRecipients(t *testing.T) {
	store := access.NewStore(t.TempDir())
	invite := func(name string, seed byte, grants ...access.Grant) presence.Person {
		m, err := store.Invite(access.InviteRequest{Name: name, Key: testKey(t, seed), Grants: grants})
		if err != nil {
			t.Fatal(err)
		}
		return presence.Person{ID: m.ID, Name: m.Name}
	}
	alice := invite("alice", 1, access.Grant{Environment: "web", Role: access.RoleOperator})
	invite("bob", 2, access.Grant{Environment: "web", Role: access.RoleOwner})
	invite("carol", 3, access.Grant{Environment: "api", Role: access.RoleObserver})
	invite("dave", 4, access.Grant{Environment: "web", Role: access.RoleObserver})

	ownerHere := false
	connected := map[string]int{}
	n := &notifier{
		store: store, owner: presence.Person{ID: presence.OwnerID, Name: "me"}, fallback: []string{fallbackOwner},
		ownerHere: func() bool { return ownerHere }, connections: func() map[string]int { return connected },
	}
	check := func(what string, owner *presence.Person, env string, want []string, wantFallback bool) {
		t.Helper()
		to, fallback := n.recipients(owner, env)
		if got := names(to); !slices.Equal(got, want) || fallback != wantFallback {
			t.Errorf("%s: told %v (fallback %v), want %v (fallback %v)", what, got, fallback, want, wantFallback)
		}
	}

	check("the owner's work, owner away", nil, "web", []string{"me"}, true)
	ownerHere = true
	check("the owner's work, owner here", nil, "web", []string{"me"}, false)

	check("alice's work, alice away", &alice, "web", []string{"me"}, true)
	connected[alice.ID] = 1
	check("alice's work, alice here", &alice, "web", []string{"alice"}, false)
	check("alice's work where she has no grant", &alice, "api", []string{"me"}, true)
	check("alice's work placed nowhere (she has no grant for all)", &alice, "", []string{"me"}, true)

	n.fallback = []string{fallbackOperators}
	ownerHere = false
	check("operators of web", nil, "web", []string{"alice", "bob"}, true)
	check("no operators in api: the owner", nil, "api", []string{"me"}, true)

	n.fallback = []string{"dave", "carol", fallbackOwner, "nobody"}
	check("names who see it, and the owner", nil, "web", []string{"dave", "me"}, true)

	if _, err := store.Revoke("alice"); err != nil {
		t.Fatal(err)
	}
	n.fallback = []string{fallbackOperators}
	check("a revoked owner is never told", &alice, "web", []string{"bob"}, true)
}
