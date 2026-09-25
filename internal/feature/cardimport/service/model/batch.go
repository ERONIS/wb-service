package model

import (
	"github.com/ERONIS/wb-service/internal/core/domain"
	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
)

const (
	BatchSchemaVersion        = cardpipeline.BatchSchemaVersion
	BatchNormalizationVersion = cardpipeline.BatchNormalizationVersion
	MaxBatchPageSize          = cardpipeline.MaxBatchPageSize
)

type BatchID = cardpipeline.BatchID
type BatchItemID = cardpipeline.BatchItemID
type Digest = cardpipeline.Digest
type SourceGroupKey = cardpipeline.SourceGroupKey
type BatchCursor = cardpipeline.BatchCursor
type TrustedActor = domain.Actor
type AuthorSnapshot = cardpipeline.AuthorSnapshot
type BatchHeader = cardpipeline.BatchHeader
type BatchItem = cardpipeline.BatchItem

func NewSourceGroupKey(
	fileID FileID,
	group string,
	vendorCode string,
) SourceGroupKey {
	return cardpipeline.NewSourceGroupKey(fileID, group, vendorCode)
}

func BatchChecksum(
	purpose Purpose,
	groupsCount int,
	items []BatchItem,
) (Digest, error) {
	return cardpipeline.BatchChecksum(purpose, groupsCount, items)
}
