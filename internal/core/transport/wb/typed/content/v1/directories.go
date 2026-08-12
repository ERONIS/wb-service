package v1

import (
	"context"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	client "github.com/ERONIS/wb-service/internal/core/transport/wb/client"
)

// DirectoriesInterface описывает справочники Content API.
type DirectoriesInterface interface {
	Colors(
		ctx context.Context,
		query contentapi.DirectoryQuery,
	) (*contentapi.DirectoryColorsResponse, error)

	Kinds(
		ctx context.Context,
		query contentapi.DirectoryQuery,
	) (*contentapi.DirectoryKindsResponse, error)

	Countries(
		ctx context.Context,
		query contentapi.DirectoryQuery,
	) (*contentapi.DirectoryCountriesResponse, error)

	Seasons(
		ctx context.Context,
		query contentapi.DirectoryQuery,
	) (*contentapi.DirectorySeasonsResponse, error)

	VAT(
		ctx context.Context,
		query contentapi.DirectoryQuery,
	) (*contentapi.DirectoryVATResponse, error)

	TNVED(
		ctx context.Context,
		query contentapi.DirectoryTNVEDQuery,
	) (*contentapi.DirectoryTNVEDResponse, error)
}

type directoriesClient struct {
	apiClient client.Executor
}

func (directories *directoriesClient) Colors(
	ctx context.Context,
	query contentapi.DirectoryQuery,
) (*contentapi.DirectoryColorsResponse, error) {
	return executeResponse[contentapi.DirectoryColorsResponse](
		ctx, directories.apiClient, contentapi.DirectoryColorsOperation(), query, nil,
	)
}

func (directories *directoriesClient) Kinds(
	ctx context.Context,
	query contentapi.DirectoryQuery,
) (*contentapi.DirectoryKindsResponse, error) {
	return executeResponse[contentapi.DirectoryKindsResponse](
		ctx, directories.apiClient, contentapi.DirectoryKindsOperation(), query, nil,
	)
}

func (directories *directoriesClient) Countries(
	ctx context.Context,
	query contentapi.DirectoryQuery,
) (*contentapi.DirectoryCountriesResponse, error) {
	return executeResponse[contentapi.DirectoryCountriesResponse](
		ctx, directories.apiClient, contentapi.DirectoryCountriesOperation(), query, nil,
	)
}

func (directories *directoriesClient) Seasons(
	ctx context.Context,
	query contentapi.DirectoryQuery,
) (*contentapi.DirectorySeasonsResponse, error) {
	return executeResponse[contentapi.DirectorySeasonsResponse](
		ctx, directories.apiClient, contentapi.DirectorySeasonsOperation(), query, nil,
	)
}

func (directories *directoriesClient) VAT(
	ctx context.Context,
	query contentapi.DirectoryQuery,
) (*contentapi.DirectoryVATResponse, error) {
	return executeResponse[contentapi.DirectoryVATResponse](
		ctx, directories.apiClient, contentapi.DirectoryVATOperation(), query, nil,
	)
}

func (directories *directoriesClient) TNVED(
	ctx context.Context,
	query contentapi.DirectoryTNVEDQuery,
) (*contentapi.DirectoryTNVEDResponse, error) {
	return executeResponse[contentapi.DirectoryTNVEDResponse](
		ctx, directories.apiClient, contentapi.DirectoryTNVEDOperation(), query, nil,
	)
}

var _ DirectoriesInterface = (*directoriesClient)(nil)
