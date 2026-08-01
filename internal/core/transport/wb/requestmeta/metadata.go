package core_transport_wb_requestmeta

import (
	"context"

	core_transport_wb_policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

type Metadata struct {
	SellerScope   string
	OperationName string
	BucketIDs     []core_transport_wb_policy.BucketID
	RetryMode     core_transport_wb_policy.RetryMode
	Attempt       int
}

type contextKey struct{}

func WithMetadata(
	ctx context.Context,
	metadata Metadata,
) context.Context {
	metadata.BucketIDs = append(
		[]core_transport_wb_policy.BucketID(nil),
		metadata.BucketIDs...,
	)

	return context.WithValue(ctx, contextKey{}, metadata)
}

func FromContext(
	ctx context.Context,
) (Metadata, bool) {
	metadata, ok := ctx.Value(contextKey{}).(Metadata)
	if !ok {
		return Metadata{}, false
	}

	metadata.BucketIDs = append(
		[]core_transport_wb_policy.BucketID(nil),
		metadata.BucketIDs...,
	)

	return metadata, true
}
