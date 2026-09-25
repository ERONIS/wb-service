package transfer_wb_transport

import (
	"context"
	"testing"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	wbcabinet_service "github.com/ERONIS/wb-service/internal/feature/wbcabinet/service"
)

func TestFanoutCredentialsRequestsPartnerAndAdminCabinets(t *testing.T) {
	expiresAt := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	source := &cabinetSourceStub{roleTargets: map[domain.UserRole][]wbcabinet_service.TargetCredential{
		domain.RolePartner: {{
			CabinetID:           "partner-cabinet",
			SellerKey:           wbcabinet_service.SellerKey{1},
			BindingRevision:     2,
			CapabilityRevision:  3,
			ContentRead:         true,
			ContentWrite:        true,
			CredentialExpiresAt: expiresAt,
			ClientGeneration:    wbcabinet_service.ClientGeneration{4},
		}},
		domain.RoleAdmin: {{
			CabinetID:           "admin-cabinet",
			SellerKey:           wbcabinet_service.SellerKey{5},
			BindingRevision:     6,
			CapabilityRevision:  7,
			ContentRead:         true,
			ContentWrite:        true,
			CredentialExpiresAt: expiresAt,
			ClientGeneration:    wbcabinet_service.ClientGeneration{8},
		}},
	}}

	credentials, err := NewTargetTransport(source).FanoutCredentials(context.Background())
	if err != nil {
		t.Fatalf("FanoutCredentials() error = %v", err)
	}
	if len(source.requestedRoles) != 2 ||
		source.requestedRoles[0] != domain.RolePartner ||
		source.requestedRoles[1] != domain.RoleAdmin {
		t.Fatalf("requested roles = %v", source.requestedRoles)
	}
	if len(credentials) != 2 || credentials[0].CabinetID != "partner-cabinet" ||
		credentials[0].SellerKey[0] != 1 || credentials[0].BindingRevision != 2 ||
		credentials[0].CapabilityRevision != 3 || !credentials[0].ContentRead ||
		!credentials[0].ContentWrite || !credentials[0].CredentialExpiresAt.Equal(expiresAt) ||
		credentials[0].ClientGeneration[0] != 4 ||
		credentials[1].CabinetID != "admin-cabinet" || credentials[1].SellerKey[0] != 5 ||
		credentials[1].BindingRevision != 6 || credentials[1].CapabilityRevision != 7 ||
		!credentials[1].ContentRead || !credentials[1].ContentWrite ||
		!credentials[1].CredentialExpiresAt.Equal(expiresAt) ||
		credentials[1].ClientGeneration[0] != 8 {
		t.Fatalf("FanoutCredentials() = %+v", credentials)
	}
}

type cabinetSourceStub struct {
	requestedRoles []domain.UserRole
	roleTargets    map[domain.UserRole][]wbcabinet_service.TargetCredential
}

func (*cabinetSourceStub) Targets(
	context.Context,
	int64,
) ([]wbcabinet_service.TargetCredential, error) {
	return nil, nil
}

func (source *cabinetSourceStub) TargetsByOwnerRole(
	_ context.Context,
	role domain.UserRole,
) ([]wbcabinet_service.TargetCredential, error) {
	source.requestedRoles = append(source.requestedRoles, role)
	return append([]wbcabinet_service.TargetCredential(nil), source.roleTargets[role]...), nil
}

func (*cabinetSourceStub) AllTargets(
	context.Context,
) ([]wbcabinet_service.TargetCredential, error) {
	return nil, nil
}

var _ CabinetSource = (*cabinetSourceStub)(nil)
