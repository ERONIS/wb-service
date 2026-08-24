package transfer_wb_transport

import (
	core_wb "github.com/ERONIS/wb-service/internal/core/transport/wb"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

type Clientset interface {
	CredentialSnapshot() ([]core_wb.CredentialIdentity, error)
}

// TargetTransport owns only single raw WB calls. Cohort readiness, sequencing
// and raw DTO interpretation belong to transfer/service.
type TargetTransport struct {
	clientset Clientset
}

func NewTargetTransport(clientset Clientset) *TargetTransport {
	if clientset == nil {
		panic("transfer WB clientset is nil")
	}
	return &TargetTransport{clientset: clientset}
}

func (transport *TargetTransport) Credentials() ([]transfer_service.TargetCredential, error) {
	credentials, err := transport.clientset.CredentialSnapshot()
	if err != nil {
		return nil, err
	}
	result := make([]transfer_service.TargetCredential, len(credentials))
	for index, credential := range credentials {
		result[index] = transfer_service.TargetCredential{
			CabinetID:           transfer_service.CabinetID(credential.CabinetID),
			BindingRevision:     credential.BindingRevision,
			CapabilityRevision:  credential.CapabilityRevision,
			ContentRead:         credential.ContentRead,
			ContentWrite:        credential.ContentWrite,
			CredentialExpiresAt: credential.CredentialExpiresAt,
		}
		copy(result[index].SellerKey[:], credential.SellerKey[:])
		copy(
			result[index].ClientGeneration[:],
			credential.ClientGeneration[:],
		)
	}
	return result, nil
}

var _ transfer_service.TargetTransport = (*TargetTransport)(nil)
