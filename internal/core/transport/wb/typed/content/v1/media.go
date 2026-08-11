package v1

import (
	"context"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	client "github.com/ERONIS/wb-service/internal/core/transport/wb/client"
)

// MediaInterface описывает загрузку медиафайлов Content API.
type MediaInterface interface {
	SaveByLinks(
		ctx context.Context,
		request contentapi.SaveMediaByLinksRequest,
	) (*contentapi.SaveMediaByLinksResponse, error)
}

type mediaClient struct {
	apiClient client.Executor
}

func (media *mediaClient) SaveByLinks(
	ctx context.Context,
	request contentapi.SaveMediaByLinksRequest,
) (*contentapi.SaveMediaByLinksResponse, error) {
	return executeResponse[contentapi.SaveMediaByLinksResponse](
		ctx, media.apiClient, contentapi.SaveMediaByLinksOperation(), nil, request,
	)
}

var _ MediaInterface = (*mediaClient)(nil)
