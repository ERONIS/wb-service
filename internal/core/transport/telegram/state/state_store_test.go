package core_tg_state

import (
	"testing"
	"time"
)

func TestStateLeaseCannotFinishReplacement(t *testing.T) {
	store := NewStateStore[string]()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }

	store.Begin(42, "first", time.Minute)
	first, claim := store.Claim(42)
	if claim != StateClaimed || first.Value != "first" {
		t.Fatalf("first claim = (%+v, %v)", first, claim)
	}
	if _, claim := store.Claim(42); claim != StateBusy {
		t.Fatalf("concurrent claim = %v, want busy", claim)
	}

	store.Begin(42, "replacement", time.Minute)
	if store.Finish(42, first, false, 0) {
		t.Fatal("stale lease finished the replacement state")
	}
	second, claim := store.Claim(42)
	if claim != StateClaimed || second.Value != "replacement" {
		t.Fatalf("replacement claim = (%+v, %v)", second, claim)
	}
	if !store.Finish(42, second, false, 0) {
		t.Fatal("current lease could not finish")
	}
	if _, claim := store.Claim(42); claim != StateMissing {
		t.Fatalf("final claim = %v, want missing", claim)
	}
}

func TestStateRetryExtendsExpiry(t *testing.T) {
	store := NewStateStore[string]()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }

	store.Begin(42, "value", time.Minute)
	lease, claim := store.Claim(42)
	if claim != StateClaimed {
		t.Fatalf("initial claim = %v", claim)
	}
	now = now.Add(50 * time.Second)
	lease.Value = "updated"
	if !store.Finish(42, lease, true, time.Minute) {
		t.Fatal("retry was rejected")
	}
	now = now.Add(50 * time.Second)
	lease, claim = store.Claim(42)
	if claim != StateClaimed || lease.Value != "updated" {
		t.Fatalf("retry claim = (%+v, %v)", lease, claim)
	}
	if !store.Finish(42, lease, true, time.Minute) {
		t.Fatal("second retry was rejected")
	}
	now = now.Add(61 * time.Second)
	if _, claim := store.Claim(42); claim != StateExpired {
		t.Fatalf("expired claim = %v, want expired", claim)
	}
}
