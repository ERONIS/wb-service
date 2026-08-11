package wb

import (
	config "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
	contentv1 "github.com/ERONIS/wb-service/internal/core/transport/wb/typed/content/v1"
)

// CabinetClient предоставляет typed API одного кабинета.
type CabinetClient struct {
	id        config.CabinetID
	name      string
	contentV1 *contentv1.ContentV1Client
}

func (client *CabinetClient) ID() config.CabinetID {
	if client == nil {
		return ""
	}

	return client.id
}

func (client *CabinetClient) Name() string {
	if client == nil {
		return ""
	}

	return client.name
}

func (client *CabinetClient) ContentV1() contentv1.ContentV1Interface {
	if client == nil {
		return nil
	}

	return client.contentV1
}
