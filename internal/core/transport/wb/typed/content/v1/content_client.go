package v1

import (
	"context"
	"errors"

	client "github.com/ERONIS/wb-service/internal/core/transport/wb/client"
	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

var errAPIClientRequired = errors.New(
	"WB Content API client is required",
)

// ContentV1Interface предоставляет типизированные группы Content API v1.
type ContentV1Interface interface {
	Categories() CategoriesInterface
	Directories() DirectoriesInterface
	Cards() CardsInterface
	Media() MediaInterface
}

// ContentV1Client объединяет типизированные клиенты Content API v1.
type ContentV1Client struct {
	categories  CategoriesInterface
	directories DirectoriesInterface
	cards       CardsInterface
	media       MediaInterface
}

// NewContentV1Client создаёт типизированный Content API поверх APIClient.
func NewContentV1Client(
	apiClient client.Executor,
) (*ContentV1Client, error) {
	if apiClient == nil {
		return nil, errAPIClientRequired
	}

	return &ContentV1Client{
		categories: &categoriesClient{
			apiClient: apiClient,
		},
		directories: &directoriesClient{
			apiClient: apiClient,
		},
		cards: &cardsClient{
			apiClient: apiClient,
		},
		media: &mediaClient{
			apiClient: apiClient,
		},
	}, nil
}

// Categories возвращает клиент категорий и характеристик.
func (content *ContentV1Client) Categories() CategoriesInterface {
	if content == nil {
		return nil
	}

	return content.categories
}

// Directories возвращает клиент справочников.
func (content *ContentV1Client) Directories() DirectoriesInterface {
	if content == nil {
		return nil
	}

	return content.directories
}

// Cards возвращает клиент карточек товаров.
func (content *ContentV1Client) Cards() CardsInterface {
	if content == nil {
		return nil
	}

	return content.cards
}

// Media возвращает клиент медиафайлов.
func (content *ContentV1Client) Media() MediaInterface {
	if content == nil {
		return nil
	}

	return content.media
}

func executeResponse[Response any](
	ctx context.Context,
	executor client.Executor,
	operation policy.Operation,
	query any,
	body any,
) (*Response, error) {
	decoded := new(Response)
	if err := executor.Execute(
		ctx,
		operation,
		query,
		body,
		decoded,
	); err != nil {
		return nil, err
	}

	return decoded, nil
}

var _ ContentV1Interface = (*ContentV1Client)(nil)
