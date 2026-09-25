package wb

import (
	"github.com/ERONIS/wb-service/internal/core/canonicalhash"
	"github.com/ERONIS/wb-service/internal/core/domain"
)

type ClientGeneration = domain.ClientGeneration

func Generation(token string) ClientGeneration {
	builder := canonicalhash.NewSHA256("wb-client-generation:v1")
	builder.String(token)
	return ClientGeneration(builder.Sum())
}
