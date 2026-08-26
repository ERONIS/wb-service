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
