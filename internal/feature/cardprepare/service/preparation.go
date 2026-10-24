package cardprepare_service

import (
	cardprepare_model "github.com/ERONIS/wb-service/internal/feature/cardprepare/service/model"
	"github.com/ERONIS/wb-service/internal/feature/cardprepare/service/preparation"

	"go.uber.org/zap"
)

func NewPreparer(transport preparation.CatalogTransport, loggers ...*zap.Logger) *preparation.Preparer {
	return preparation.NewPreparer(transport, loggers...)
}

func Prepare(group cardprepare_model.SourceGroup, snapshot cardprepare_model.CatalogSnapshot) (cardprepare_model.Result, error) {
	return preparation.Prepare(group, snapshot)
}
