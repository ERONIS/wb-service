package transfer_wb_transport

import (
	"context"
	"errors"
	"fmt"
	"time"

	core_wb "github.com/ERONIS/wb-service/internal/core/transport/wb"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	client "github.com/ERONIS/wb-service/internal/core/transport/wb/client"
	wbconfig "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

type Clientset interface {
	CredentialSnapshot(now time.Time) ([]core_wb.CredentialIdentity, error)
	PinnedExecutor(
		id wbconfig.CabinetID,
		generation core_wb.ClientGeneration,
	) (client.Executor, error)
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

func (transport *TargetTransport) Credentials(
	now time.Time,
) ([]transfer_service.TargetCredential, error) {
	credentials, err := transport.clientset.CredentialSnapshot(now)
	if err != nil {
		return nil, err
	}
	result := make([]transfer_service.TargetCredential, len(credentials))
	for index, credential := range credentials {
		result[index] = transfer_service.TargetCredential{
			CabinetID:           transfer_service.CabinetID(credential.CabinetID),
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

func (transport *TargetTransport) ProbeCardsList(
	ctx context.Context,
	cabinetID transfer_service.CabinetID,
	generation transfer_service.ClientGeneration,
) (contentapi.CardsListResponse, error) {
	if ctx == nil {
		return contentapi.CardsListResponse{}, errors.New(
			"probe WB Cards List: context is nil",
		)
	}
	var coreGeneration core_wb.ClientGeneration
	copy(coreGeneration[:], generation[:])
	executor, err := transport.clientset.PinnedExecutor(
		wbconfig.CabinetID(cabinetID),
		coreGeneration,
	)
	if err != nil {
		return contentapi.CardsListResponse{}, fmt.Errorf(
			"pin cabinet executor: %w",
			err,
		)
	}

	request := contentapi.CardsListRequest{
		Settings: contentapi.CardsListSettings{
			Sort:   contentapi.CardsSort{Ascending: true},
			Cursor: contentapi.CardsListCursor{Limit: 1},
		},
	}
	return client.ExecuteResponse[contentapi.CardsListResponse](
		ctx,
		executor,
		contentapi.CardsListOperation(),
		nil,
		request,
	)
}

var _ transfer_service.TargetTransport = (*TargetTransport)(nil)
