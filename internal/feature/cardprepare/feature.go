package cardprepare

import (
	"context"

	core_domain "github.com/ERONIS/wb-service/internal/core/domain"
	cardprepare_catalog "github.com/ERONIS/wb-service/internal/feature/cardprepare/service/catalog"
	cardprepare_model "github.com/ERONIS/wb-service/internal/feature/cardprepare/service/model"

	"go.uber.org/zap"
)

type Feature struct {
	preparer *cardprepare_catalog.Preparer
}

func New(
	catalogTransport cardprepare_catalog.CatalogTransport,
	loggers ...*zap.Logger,
) *Feature {
	return &Feature{
		preparer: cardprepare_catalog.NewPreparer(catalogTransport, loggers...),
	}
}

func (feature *Feature) Preparer() *cardprepare_catalog.Preparer {
	return feature.preparer
}

func (feature *Feature) PrepareTarget(
	ctx context.Context,
	sellerID core_domain.SellerID,
	group cardprepare_model.SourceGroup,
) (cardprepare_model.Result, error) {
	return feature.preparer.PrepareTarget(ctx, sellerID, group)
}
