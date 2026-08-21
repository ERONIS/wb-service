package cardprepare_wb_transport

import (
	"context"
	"fmt"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	client "github.com/ERONIS/wb-service/internal/core/transport/wb/client"
	wbconfig "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
	cardprepare_service "github.com/ERONIS/wb-service/internal/feature/cardprepare/service"
)

type ExecutorRegistry interface {
	ExecutorForCabinet(id wbconfig.CabinetID) (client.Executor, error)
}

// CatalogTransport contains no pagination, cache or response interpretation.
// Each method selects one closed operation and returns its complete raw DTO.
type CatalogTransport struct {
	executors ExecutorRegistry
}

func NewCatalogTransport(executors ExecutorRegistry) *CatalogTransport {
	if executors == nil {
		panic("cardprepare WB executor registry is nil")
	}
	return &CatalogTransport{executors: executors}
}

func (transport *CatalogTransport) CardsLimits(
	ctx context.Context,
	cabinetID cardprepare_service.CabinetID,
) (contentapi.CardsLimitsResponse, error) {
	return executeRaw[contentapi.CardsLimitsResponse](
		ctx, transport.executors, cabinetID,
		contentapi.CardsLimitsOperation(), nil, nil,
	)
}

func (transport *CatalogTransport) Subjects(
	ctx context.Context,
	cabinetID cardprepare_service.CabinetID,
	query contentapi.SubjectsQuery,
) (contentapi.SubjectsResponse, error) {
	return executeRaw[contentapi.SubjectsResponse](
		ctx, transport.executors, cabinetID,
		contentapi.SubjectsOperation(), query, nil,
	)
}

func (transport *CatalogTransport) SubjectCharacteristics(
	ctx context.Context,
	cabinetID cardprepare_service.CabinetID,
	subjectID int64,
	query contentapi.SubjectCharacteristicsQuery,
) (contentapi.SubjectCharacteristicsResponse, error) {
	operation, err := contentapi.SubjectCharacteristicsOperation(subjectID)
	if err != nil {
		return contentapi.SubjectCharacteristicsResponse{}, err
	}
	return executeRaw[contentapi.SubjectCharacteristicsResponse](
		ctx, transport.executors, cabinetID, operation, query, nil,
	)
}

func (transport *CatalogTransport) Brands(
	ctx context.Context,
	cabinetID cardprepare_service.CabinetID,
	query contentapi.BrandsQuery,
) (contentapi.BrandsResponse, error) {
	return executeRaw[contentapi.BrandsResponse](
		ctx, transport.executors, cabinetID,
		contentapi.BrandsOperation(), query, nil,
	)
}

func (transport *CatalogTransport) Colors(
	ctx context.Context,
	cabinetID cardprepare_service.CabinetID,
	query contentapi.DirectoryQuery,
) (contentapi.DirectoryColorsResponse, error) {
	return executeRaw[contentapi.DirectoryColorsResponse](
		ctx, transport.executors, cabinetID,
		contentapi.DirectoryColorsOperation(), query, nil,
	)
}

func (transport *CatalogTransport) Kinds(
	ctx context.Context,
	cabinetID cardprepare_service.CabinetID,
	query contentapi.DirectoryQuery,
) (contentapi.DirectoryKindsResponse, error) {
	return executeRaw[contentapi.DirectoryKindsResponse](
		ctx, transport.executors, cabinetID,
		contentapi.DirectoryKindsOperation(), query, nil,
	)
}

func (transport *CatalogTransport) Countries(
	ctx context.Context,
	cabinetID cardprepare_service.CabinetID,
	query contentapi.DirectoryQuery,
) (contentapi.DirectoryCountriesResponse, error) {
	return executeRaw[contentapi.DirectoryCountriesResponse](
		ctx, transport.executors, cabinetID,
		contentapi.DirectoryCountriesOperation(), query, nil,
	)
}

func (transport *CatalogTransport) Seasons(
	ctx context.Context,
	cabinetID cardprepare_service.CabinetID,
	query contentapi.DirectoryQuery,
) (contentapi.DirectorySeasonsResponse, error) {
	return executeRaw[contentapi.DirectorySeasonsResponse](
		ctx, transport.executors, cabinetID,
		contentapi.DirectorySeasonsOperation(), query, nil,
	)
}

func (transport *CatalogTransport) VAT(
	ctx context.Context,
	cabinetID cardprepare_service.CabinetID,
	query contentapi.DirectoryQuery,
) (contentapi.DirectoryVATResponse, error) {
	return executeRaw[contentapi.DirectoryVATResponse](
		ctx, transport.executors, cabinetID,
		contentapi.DirectoryVATOperation(), query, nil,
	)
}

func (transport *CatalogTransport) TNVED(
	ctx context.Context,
	cabinetID cardprepare_service.CabinetID,
	query contentapi.DirectoryTNVEDQuery,
) (contentapi.DirectoryTNVEDResponse, error) {
	return executeRaw[contentapi.DirectoryTNVEDResponse](
		ctx, transport.executors, cabinetID,
		contentapi.DirectoryTNVEDOperation(), query, nil,
	)
}

func executeRaw[Response any](
	ctx context.Context,
	executors ExecutorRegistry,
	cabinetID cardprepare_service.CabinetID,
	operation policy.Operation,
	query any,
	body any,
) (Response, error) {
	executor, err := executors.ExecutorForCabinet(
		wbconfig.CabinetID(cabinetID),
	)
	if err != nil {
		var response Response
		return response, fmt.Errorf("get WB cabinet executor: %w", err)
	}
	return client.ExecuteResponse[Response](ctx, executor, operation, query, body)
}

var _ cardprepare_service.CatalogTransport = (*CatalogTransport)(nil)
