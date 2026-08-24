package identity

import (
	"context"
	"time"

	config "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
)

// Verifier authenticates the exact credential against WB and returns the
// seller identity asserted by WB, not merely decoded from the JWT payload.
type Verifier interface {
	Verify(
		ctx context.Context,
		cabinetID config.CabinetID,
		generation ClientGeneration,
	) (RemoteIdentity, error)
}

// Store owns the durable CabinetID-to-SellerKey binding and its revisions.
type Store interface {
	SyncBindings(
		ctx context.Context,
		verifiedAt time.Time,
		credentials []VerifiedCredential,
	) ([]Binding, error)
}
