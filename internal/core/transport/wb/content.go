package wb

import (
	"context"

	"github.com/ERONIS/wb-service/internal/core/domain"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	core_wb_request "github.com/ERONIS/wb-service/internal/core/transport/wb/client/request"
)

// ContentClient предоставляет типизированные операции Content API для фич.
type ContentClient struct{ executors PinnedExecutorRegistry }

func NewContentClient(executors PinnedExecutorRegistry) *ContentClient {
	if executors == nil {
		panic("WB executor registry is nil")
	}
	return &ContentClient{executors: executors}
}

func (transport *ContentClient) CardsLimits(
	ctx context.Context,
	cabinetID domain.CabinetID,
) (contentapi.CardsLimitsResponse, error) {
	return ExecuteForCabinet[contentapi.CardsLimitsResponse](
		ctx, transport.executors, cabinetID,
		contentapi.CardsLimitsOperation(), nil, nil,
	)
}

func (transport *ContentClient) Subjects(
	ctx context.Context,
	cabinetID domain.CabinetID,
	query contentapi.SubjectsQuery,
) (contentapi.SubjectsResponse, error) {
	return ExecuteForCabinet[contentapi.SubjectsResponse](
		ctx, transport.executors, cabinetID,
		contentapi.SubjectsOperation(), query, nil,
	)
}

func (transport *ContentClient) SubjectCharacteristics(
	ctx context.Context,
	cabinetID domain.CabinetID,
	subjectID int64,
	query contentapi.SubjectCharacteristicsQuery,
) (contentapi.SubjectCharacteristicsResponse, error) {
	operation, err := contentapi.SubjectCharacteristicsOperation(subjectID)
	if err != nil {
		return contentapi.SubjectCharacteristicsResponse{}, err
	}
	return ExecuteForCabinet[contentapi.SubjectCharacteristicsResponse](
		ctx, transport.executors, cabinetID, operation, query, nil,
	)
}

func (transport *ContentClient) Brands(
	ctx context.Context,
	cabinetID domain.CabinetID,
	query contentapi.BrandsQuery,
) (contentapi.BrandsResponse, error) {
	return ExecuteForCabinet[contentapi.BrandsResponse](
		ctx, transport.executors, cabinetID,
		contentapi.BrandsOperation(), query, nil,
	)
}

func (transport *ContentClient) Colors(
	ctx context.Context,
	cabinetID domain.CabinetID,
	query contentapi.DirectoryQuery,
) (contentapi.DirectoryColorsResponse, error) {
	return ExecuteForCabinet[contentapi.DirectoryColorsResponse](
		ctx, transport.executors, cabinetID,
		contentapi.DirectoryColorsOperation(), query, nil,
	)
}

func (transport *ContentClient) Kinds(
	ctx context.Context,
	cabinetID domain.CabinetID,
	query contentapi.DirectoryQuery,
) (contentapi.DirectoryKindsResponse, error) {
	return ExecuteForCabinet[contentapi.DirectoryKindsResponse](
		ctx, transport.executors, cabinetID,
		contentapi.DirectoryKindsOperation(), query, nil,
	)
}

func (transport *ContentClient) Countries(
	ctx context.Context,
	cabinetID domain.CabinetID,
	query contentapi.DirectoryQuery,
) (contentapi.DirectoryCountriesResponse, error) {
	return ExecuteForCabinet[contentapi.DirectoryCountriesResponse](
		ctx, transport.executors, cabinetID,
		contentapi.DirectoryCountriesOperation(), query, nil,
	)
}

func (transport *ContentClient) Seasons(
	ctx context.Context,
	cabinetID domain.CabinetID,
	query contentapi.DirectoryQuery,
) (contentapi.DirectorySeasonsResponse, error) {
	return ExecuteForCabinet[contentapi.DirectorySeasonsResponse](
		ctx, transport.executors, cabinetID,
		contentapi.DirectorySeasonsOperation(), query, nil,
	)
}

func (transport *ContentClient) VAT(
	ctx context.Context,
	cabinetID domain.CabinetID,
	query contentapi.DirectoryQuery,
) (contentapi.DirectoryVATResponse, error) {
	return ExecuteForCabinet[contentapi.DirectoryVATResponse](
		ctx, transport.executors, cabinetID,
		contentapi.DirectoryVATOperation(), query, nil,
	)
}

func (transport *ContentClient) TNVED(
	ctx context.Context,
	cabinetID domain.CabinetID,
	query contentapi.DirectoryTNVEDQuery,
) (contentapi.DirectoryTNVEDResponse, error) {
	return ExecuteForCabinet[contentapi.DirectoryTNVEDResponse](
		ctx, transport.executors, cabinetID,
		contentapi.DirectoryTNVEDOperation(), query, nil,
	)
}

func (transport *ContentClient) CardsList(
	ctx context.Context,
	cabinetID domain.CabinetID,
	query contentapi.CardsListQuery,
	request contentapi.CardsListRequest,
) (contentapi.CardsListResponse, error) {
	return ExecuteForCabinet[contentapi.CardsListResponse](
		ctx,
		transport.executors,
		cabinetID,
		contentapi.CardsListOperation(),
		query,
		request,
	)
}

func (transport *ContentClient) TrashCardsList(
	ctx context.Context,
	cabinetID domain.CabinetID,
	query contentapi.TrashCardsListQuery,
	request contentapi.TrashCardsListRequest,
) (contentapi.TrashCardsListResponse, error) {
	return ExecuteForCabinet[contentapi.TrashCardsListResponse](
		ctx,
		transport.executors,
		cabinetID,
		contentapi.TrashCardsListOperation(),
		query,
		request,
	)
}

func (transport *ContentClient) CardsErrorList(
	ctx context.Context,
	cabinetID domain.CabinetID,
	query contentapi.CardsErrorListQuery,
	request contentapi.CardsErrorListRequest,
) (contentapi.CardsErrorListResponse, error) {
	return ExecuteForCabinet[contentapi.CardsErrorListResponse](
		ctx,
		transport.executors,
		cabinetID,
		contentapi.CardsErrorListOperation(),
		query,
		request,
	)
}

func (transport *ContentClient) UploadCards(
	ctx context.Context,
	cabinetID domain.CabinetID,
	generation domain.ClientGeneration,
	request contentapi.UploadCardsRequest,
) (contentapi.UploadCardsResponse, error) {
	return ExecutePinnedForCabinet[contentapi.UploadCardsResponse](
		ctx,
		transport.executors,
		cabinetID,
		generation,
		contentapi.UploadCardsOperation(),
		nil,
		request,
	)
}

func (transport *ContentClient) UploadCardsAdd(
	ctx context.Context,
	cabinetID domain.CabinetID,
	generation domain.ClientGeneration,
	request contentapi.UploadCardsAddRequest,
) (contentapi.UploadCardsAddResponse, error) {
	return ExecutePinnedForCabinet[contentapi.UploadCardsAddResponse](
		ctx,
		transport.executors,
		cabinetID,
		generation,
		contentapi.UploadCardsAddOperation(),
		nil,
		request,
	)
}

func (transport *ContentClient) SaveMediaByLinks(
	ctx context.Context,
	cabinetID domain.CabinetID,
	generation domain.ClientGeneration,
	request contentapi.SaveMediaByLinksRequest,
) (contentapi.SaveMediaByLinksResponse, error) {
	return ExecutePinnedForCabinet[contentapi.SaveMediaByLinksResponse](
		ctx,
		transport.executors,
		cabinetID,
		generation,
		contentapi.SaveMediaByLinksOperation(),
		nil,
		request,
	)
}

func (transport *ContentClient) UploadMediaFile(
	ctx context.Context,
	cabinetID domain.CabinetID,
	generation domain.ClientGeneration,
	request contentapi.UploadMediaFileRequest,
) (contentapi.UploadMediaFileResponse, error) {
	operation, err := contentapi.UploadMediaFileOperation(request.NMID, request.MediaNumber)
	if err != nil {
		return contentapi.UploadMediaFileResponse{}, err
	}
	return ExecutePinnedForCabinet[contentapi.UploadMediaFileResponse](
		ctx,
		transport.executors,
		cabinetID,
		generation,
		operation,
		nil,
		core_wb_request.MultipartFile{
			FieldName: "uploadfile",
			FileName:  request.FileName,
			Data:      request.Data,
		},
	)
}

func (transport *ContentClient) UpdateCards(
	ctx context.Context,
	cabinetID domain.CabinetID,
	generation domain.ClientGeneration,
	request contentapi.UpdateCardsRequest,
) (contentapi.UpdateCardsResponse, error) {
	return ExecutePinnedForCabinet[contentapi.UpdateCardsResponse](
		ctx, transport.executors, cabinetID, generation,
		contentapi.UpdateCardsOperation(), nil, request,
	)
}

func (transport *ContentClient) Tags(ctx context.Context, cabinetID domain.CabinetID) (contentapi.TagsResponse, error) {
	return ExecuteForCabinet[contentapi.TagsResponse](ctx, transport.executors, cabinetID, contentapi.TagsOperation(), nil, nil)
}
