package cardpublication_wb_transport

import (
	core_wb "github.com/ERONIS/wb-service/internal/core/transport/wb"
	cardpublication_service "github.com/ERONIS/wb-service/internal/feature/cardpublication/service"
)

type CatalogTransport struct {
	*core_wb.ContentClient
	*MediaDownloader
}

func NewCatalogTransport(executors core_wb.PinnedExecutorRegistry) *CatalogTransport {
	return &CatalogTransport{ContentClient: core_wb.NewContentClient(executors), MediaDownloader: NewMediaDownloader()}
}

var _ cardpublication_service.CatalogTransport = (*CatalogTransport)(nil)
