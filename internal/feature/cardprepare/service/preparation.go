package cardprepare_service

import (
	"github.com/ERONIS/wb-service/internal/feature/cardprepare/service/preparation"
	"go.uber.org/zap"
)

type CatalogTransport = preparation.CatalogTransport
type Preparer = preparation.Preparer

func NewPreparer(transport CatalogTransport, loggers ...*zap.Logger) *Preparer {
	return preparation.NewPreparer(transport, loggers...)
}

func Prepare(group SourceGroup, snapshot CatalogSnapshot) (Result, error) {
	return preparation.Prepare(group, snapshot)
}
