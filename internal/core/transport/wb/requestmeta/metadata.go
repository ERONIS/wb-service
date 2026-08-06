package core_transport_wb_requestmeta

import (
	"context"

	core_transport_wb_policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

type Metadata struct {
	SellerScope   string
	OperationName string
	BucketID      core_transport_wb_policy.BucketID
	RetryMode     core_transport_wb_policy.RetryMode
	Attempt       int
}

type contextKey struct{}

func WithMetadata(
	ctx context.Context,
	metadata Metadata,
) context.Context {
	return context.WithValue(
		ctx,
		contextKey{},
		metadata,
	)
}

func FromContext(
	ctx context.Context,
) (Metadata, bool) {
	metadata, ok :=
		ctx.Value(contextKey{}).(Metadata)

	return metadata, ok
}
