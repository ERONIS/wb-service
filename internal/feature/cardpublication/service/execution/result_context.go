package execution

import (
	"context"
	"time"
)

const publicationResultTimeout = 10 * time.Second

// Once a mutation has been attempted, persist its evidence even during shutdown.
// Only the result transaction may outlive cancellation; WB calls never do.
func publicationResultContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), publicationResultTimeout)
}
