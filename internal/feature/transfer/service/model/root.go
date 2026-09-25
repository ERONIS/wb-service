package model

import (
	"github.com/ERONIS/wb-service/internal/core/canonicalhash"
)

func TargetSetRoot(snapshot MutationTargetSnapshot) Digest {
	builder := canonicalhash.NewSHA256("transfer-target-set:v2")
	builder.String(snapshot.CohortName)
	builder.Bytes(snapshot.Revision[:])
	builder.Int64(int64(len(snapshot.Targets)))
	for _, target := range snapshot.Targets {
		builder.Int64(int64(target.Position))
		builder.String(string(target.CabinetID))
		builder.Bytes(target.SellerKey[:])
		builder.Bytes(target.ClientGeneration[:])
		builder.Int64(target.CredentialExpiresAt.UTC().UnixNano())
		builder.Int64(target.BindingRevision)
		builder.Int64(target.CapabilityRevision)
		builder.Bool(target.ContentRead)
		builder.Bool(target.ContentWrite)
	}
	return Digest(builder.Sum())
}
