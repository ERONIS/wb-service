package identity

import (
	"crypto/sha256"
	"time"

	config "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
)

type SellerKey [sha256.Size]byte
type ClientGeneration [sha256.Size]byte

// Credential is the secret-bearing input used only while the registry is
// initialized. Registry snapshots never expose the raw token.
type Credential struct {
	CabinetID config.CabinetID
	Token     string
}

// RemoteIdentity contains identity data authenticated by WB itself.
type RemoteIdentity struct {
	SellerID string
}

// VerifiedCredential is produced only after the exact token has passed the
// remote WB checks and its local claims agree with WB's seller identity.
type VerifiedCredential struct {
	CabinetID           config.CabinetID
	SellerKey           SellerKey
	ContentRead         bool
	ContentWrite        bool
	CredentialExpiresAt time.Time
	ClientGeneration    ClientGeneration
}

// Binding adds persistent identity and capability revisions to a verified
// credential. It is the safe credential view consumed by WB features.
type Binding struct {
	CabinetID           config.CabinetID
	SellerKey           SellerKey
	BindingRevision     int64
	CapabilityRevision  int64
	ContentRead         bool
	ContentWrite        bool
	CredentialExpiresAt time.Time
	ClientGeneration    ClientGeneration
}
