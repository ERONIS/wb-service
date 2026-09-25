package execution

import (
	"context"
	"time"
)

const publicationResultTimeout = 10 * time.Second

func publicationResultContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), publicationResultTimeout)
}
