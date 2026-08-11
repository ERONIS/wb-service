package v1

import (
	"context"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	client "github.com/ERONIS/wb-service/internal/core/transport/wb/client"
)

// CategoriesInterface описывает категории и характеристики Content API.
type CategoriesInterface interface {
	ParentCategories(
		ctx context.Context,
		query contentapi.ParentCategoriesQuery,
	) (*contentapi.ParentCategoriesResponse, error)

	Subjects(
		ctx context.Context,
		query contentapi.SubjectsQuery,
	) (*contentapi.SubjectsResponse, error)

	SubjectCharacteristics(
		ctx context.Context,
		subjectID int64,
		query contentapi.SubjectCharacteristicsQuery,
	) (*contentapi.SubjectCharacteristicsResponse, error)

	Brands(
		ctx context.Context,
		query contentapi.BrandsQuery,
	) (*contentapi.BrandsResponse, error)
}

type categoriesClient struct {
	apiClient client.Executor
}

func (categories *categoriesClient) ParentCategories(
	ctx context.Context,
	query contentapi.ParentCategoriesQuery,
) (*contentapi.ParentCategoriesResponse, error) {
	return executeResponse[contentapi.ParentCategoriesResponse](
		ctx, categories.apiClient, contentapi.ParentCategoriesOperation(), query, nil,
	)
}

func (categories *categoriesClient) Subjects(
	ctx context.Context,
	query contentapi.SubjectsQuery,
) (*contentapi.SubjectsResponse, error) {
	return executeResponse[contentapi.SubjectsResponse](
		ctx, categories.apiClient, contentapi.SubjectsOperation(), query, nil,
	)
}

func (categories *categoriesClient) SubjectCharacteristics(
	ctx context.Context,
	subjectID int64,
	query contentapi.SubjectCharacteristicsQuery,
) (*contentapi.SubjectCharacteristicsResponse, error) {
	operation, err := contentapi.SubjectCharacteristicsOperation(
		subjectID,
	)
	if err != nil {
		return nil, err
	}

	return executeResponse[contentapi.SubjectCharacteristicsResponse](
		ctx, categories.apiClient, operation, query, nil,
	)
}

func (categories *categoriesClient) Brands(
	ctx context.Context,
	query contentapi.BrandsQuery,
) (*contentapi.BrandsResponse, error) {
	return executeResponse[contentapi.BrandsResponse](
		ctx, categories.apiClient, contentapi.BrandsOperation(), query, nil,
	)
}

var _ CategoriesInterface = (*categoriesClient)(nil)
