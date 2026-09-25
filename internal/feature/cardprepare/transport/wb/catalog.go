package cardprepare_wb_transport

import (
	"context"

	core_wb "github.com/ERONIS/wb-service/internal/core/transport/wb"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	cardprepare_service "github.com/ERONIS/wb-service/internal/feature/cardprepare/service"
)

// CatalogTransport contains no pagination, cache or response interpretation.
// Each method selects one closed operation and returns its complete raw DTO.
type CatalogTransport struct {
	executors core_wb.ExecutorRegistry
}

func NewCatalogTransport(executors core_wb.ExecutorRegistry) *CatalogTransport {
	if executors == nil {
		panic("cardprepare WB executor registry is nil")
	}
	return &CatalogTransport{executors: executors}
}

func (transport *CatalogTransport) CardsLimits(
	ctx context.Context,
	cabinetID cardprepare_service.CabinetID,
) (contentapi.CardsLimitsResponse, error) {
	return core_wb.ExecuteForCabinet[contentapi.CardsLimitsResponse](
		ctx, transport.executors, cabinetID,
		contentapi.CardsLimitsOperation(), nil, nil,
	)
}

func (transport *CatalogTransport) Subjects(
	ctx context.Context,
	cabinetID cardprepare_service.CabinetID,
	query contentapi.SubjectsQuery,
) (contentapi.SubjectsResponse, error) {
	return core_wb.ExecuteForCabinet[contentapi.SubjectsResponse](
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
	return core_wb.ExecuteForCabinet[contentapi.SubjectCharacteristicsResponse](
		ctx, transport.executors, cabinetID, operation, query, nil,
	)
}

func (transport *CatalogTransport) Brands(
	ctx context.Context,
	cabinetID cardprepare_service.CabinetID,
	query contentapi.BrandsQuery,
) (contentapi.BrandsResponse, error) {
	return core_wb.ExecuteForCabinet[contentapi.BrandsResponse](
		ctx, transport.executors, cabinetID,
		contentapi.BrandsOperation(), query, nil,
	)
}

func (transport *CatalogTransport) Colors(
	ctx context.Context,
	cabinetID cardprepare_service.CabinetID,
	query contentapi.DirectoryQuery,
) (contentapi.DirectoryColorsResponse, error) {
	return core_wb.ExecuteForCabinet[contentapi.DirectoryColorsResponse](
		ctx, transport.executors, cabinetID,
		contentapi.DirectoryColorsOperation(), query, nil,
	)
}

func (transport *CatalogTransport) Kinds(
	ctx context.Context,
	cabinetID cardprepare_service.CabinetID,
	query contentapi.DirectoryQuery,
) (contentapi.DirectoryKindsResponse, error) {
	return core_wb.ExecuteForCabinet[contentapi.DirectoryKindsResponse](
		ctx, transport.executors, cabinetID,
		contentapi.DirectoryKindsOperation(), query, nil,
	)
}

func (transport *CatalogTransport) Countries(
	ctx context.Context,
	cabinetID cardprepare_service.CabinetID,
	query contentapi.DirectoryQuery,
) (contentapi.DirectoryCountriesResponse, error) {
	return core_wb.ExecuteForCabinet[contentapi.DirectoryCountriesResponse](
		ctx, transport.executors, cabinetID,
		contentapi.DirectoryCountriesOperation(), query, nil,
	)
}

func (transport *CatalogTransport) Seasons(
	ctx context.Context,
	cabinetID cardprepare_service.CabinetID,
	query contentapi.DirectoryQuery,
) (contentapi.DirectorySeasonsResponse, error) {
	return core_wb.ExecuteForCabinet[contentapi.DirectorySeasonsResponse](
		ctx, transport.executors, cabinetID,
		contentapi.DirectorySeasonsOperation(), query, nil,
	)
}

func (transport *CatalogTransport) VAT(
	ctx context.Context,
	cabinetID cardprepare_service.CabinetID,
	query contentapi.DirectoryQuery,
) (contentapi.DirectoryVATResponse, error) {
	return core_wb.ExecuteForCabinet[contentapi.DirectoryVATResponse](
		ctx, transport.executors, cabinetID,
		contentapi.DirectoryVATOperation(), query, nil,
	)
}

func (transport *CatalogTransport) TNVED(
	ctx context.Context,
	cabinetID cardprepare_service.CabinetID,
	query contentapi.DirectoryTNVEDQuery,
) (contentapi.DirectoryTNVEDResponse, error) {
	return core_wb.ExecuteForCabinet[contentapi.DirectoryTNVEDResponse](
		ctx, transport.executors, cabinetID,
		contentapi.DirectoryTNVEDOperation(), query, nil,
	)
}

var _ cardprepare_service.CatalogTransport = (*CatalogTransport)(nil)
