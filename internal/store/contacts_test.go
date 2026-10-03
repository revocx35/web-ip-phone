package store

import (
	"context"
	"errors"
	"testing"
)

func names(cs []*Contact) []string {
	out := []string{}
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

func TestContacts(t *testing.T) {
	ctx := context.Background()
	s := mustStore(t)
	alice, bob := user(t, s, "alice", false), user(t, s, "bob", false)

	aID := must(s.CreateContact(ctx, &Contact{OwnerID: alice.ID, Name: "zed", Numbers: []ContactNumber{{Label: "Mobile", Number: "+4915112345678"}, {Number: "1005"}}}))
	must(s.CreateContact(ctx, &Contact{OwnerID: bob.ID, Name: "Bob's dentist", Numbers: []ContactNumber{{Number: "0301234"}}}))
	shared := must(s.CreateContact(ctx, &Contact{Name: "Reception", Numbers: []ContactNumber{{Number: "2001"}}}))

	// own + shared, sorted by name without case; numbers in order
	got := must(s.ListContacts(ctx, alice.ID))
	if n := names(got); len(n) != 2 || n[0] != "Reception" || n[1] != "zed" {
		t.Fatalf("alice sees %v", n)
	}
	if z := got[1]; len(z.Numbers) != 2 || z.Numbers[0].Label != "Mobile" || z.Numbers[1].Number != "1005" || z.Shared() || !got[0].Shared() {
		t.Fatalf("contact: %+v", z)
	}
	if _, err := s.GetContact(ctx, bob.ID, aID); !errors.Is(err, ErrNotFound) {
		t.Fatal("bob can read alice's contact")
	}
	if err := s.SetFavorite(ctx, bob.ID, aID, true); !errors.Is(err, ErrNotFound) {
		t.Fatal("bob can favorite alice's contact")
	}

	// favorites are per user, also on shared contacts
	if err := s.SetFavorite(ctx, alice.ID, shared, true); err != nil {
		t.Fatal(err)
	}
	s.SetFavorite(ctx, alice.ID, shared, true) // idempotent
	if c := must(s.GetContact(ctx, alice.ID, shared)); !c.Favorite {
		t.Fatal("favorite not stored")
	}
	if c := must(s.GetContact(ctx, bob.ID, shared)); c.Favorite {
		t.Fatal("alice's favorite shows for bob")
	}

	// update replaces the numbers; unsharing drops other users' favorites
	c := must(s.GetContact(ctx, alice.ID, shared))
	c.OwnerID, c.Name, c.Numbers = bob.ID, "Front desk", []ContactNumber{{Label: "Desk", Number: "2002"}}
	if err := s.UpdateContact(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetContact(ctx, alice.ID, shared); !errors.Is(err, ErrNotFound) {
		t.Fatal("unshared contact still visible to alice")
	}
	c = must(s.GetContact(ctx, bob.ID, shared))
	if c.Name != "Front desk" || len(c.Numbers) != 1 || c.Numbers[0].Number != "2002" {
		t.Fatalf("updated: %+v", c)
	}
	c.OwnerID = 0
	s.UpdateContact(ctx, c)
	if c := must(s.GetContact(ctx, alice.ID, shared)); c.Favorite {
		t.Fatal("old favorite came back after re-sharing")
	}
	if n := must(s.CountContacts(ctx, 0)); n != 1 {
		t.Fatalf("shared count %d", n)
	}
	if n := must(s.CountContacts(ctx, alice.ID)); n != 1 {
		t.Fatalf("alice count %d", n)
	}

	// deleting a user removes their contacts and favorites
	s.SetFavorite(ctx, bob.ID, shared, true)
	if err := s.DeleteUser(ctx, bob.ID); err != nil {
		t.Fatal(err)
	}
	if n := must(s.CountContacts(ctx, bob.ID)); n != 0 {
		t.Fatal("contacts of a deleted user remain")
	}
	if err := s.DeleteContact(ctx, aID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteContact(ctx, aID); !errors.Is(err, ErrNotFound) {
		t.Fatal("double delete")
	}
	var rows int
	s.db.QueryRow("SELECT (SELECT COUNT(*) FROM contact_numbers WHERE contact_id = ?) + (SELECT COUNT(*) FROM contact_favorites WHERE user_id = ?)",
		aID, bob.ID).Scan(&rows)
	if rows != 0 {
		t.Fatal("numbers or favorites left behind")
	}
}
