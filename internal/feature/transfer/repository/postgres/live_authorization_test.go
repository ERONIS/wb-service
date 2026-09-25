package transfer_postgres_repository

import (
	"testing"

	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

func TestLiveAuthorizationCommandKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state transfer_service.LiveAuthorizationState
		want  string
	}{
		{name: "supersede", state: transfer_service.LiveAuthorizationSuperseded, want: "supersede"},
		{name: "close", state: transfer_service.LiveAuthorizationClosed, want: "close"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := liveAuthorizationCommandKind(test.state)
			if err != nil {
				t.Fatalf("liveAuthorizationCommandKind() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("liveAuthorizationCommandKind() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestLiveAuthorizationCommandKindRejectsUnsupportedState(t *testing.T) {
	t.Parallel()

	if _, err := liveAuthorizationCommandKind(transfer_service.LiveAuthorizationAuthorized); err == nil {
		t.Fatal("liveAuthorizationCommandKind() error = nil, want non-nil")
	}
}

func TestTargetOwnerMayMutate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		sourceKind string
		ownerRole  string
		want       bool
	}{
		{name: "xlsx partner", sourceKind: "xlsx", ownerRole: "partner", want: true},
		{name: "xlsx user", sourceKind: "xlsx", ownerRole: "user", want: false},
		{name: "xlsx admin", sourceKind: "xlsx", ownerRole: "admin", want: true},
		{name: "cabinet copy remains owner scoped", sourceKind: "wb_cabinet", ownerRole: "admin", want: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := targetOwnerMayMutate(test.sourceKind, test.ownerRole); got != test.want {
				t.Fatalf("targetOwnerMayMutate(%q, %q) = %t, want %t", test.sourceKind, test.ownerRole, got, test.want)
			}
		})
	}
}
