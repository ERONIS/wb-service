package wb

import (
	"github.com/ERONIS/wb-service/internal/core/canonicalhash"
	"github.com/ERONIS/wb-service/internal/core/domain"
)

// ClientGeneration pins an operation to the exact credential that was
// activated for a cabinet. It is safe to persist and never exposes the token.
type ClientGeneration = domain.ClientGeneration

// Generation returns a domain-separated digest of a WB credential.
func Generation(token string) ClientGeneration {
	builder := canonicalhash.NewSHA256("wb-client-generation:v1")
	builder.String(token)
	return ClientGeneration(builder.Sum())
}
