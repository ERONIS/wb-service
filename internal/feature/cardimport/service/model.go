package cardimport_service

import "github.com/ERONIS/wb-service/internal/feature/cardimport/service/model"

type SessionID = model.SessionID
type Purpose = model.Purpose
type SessionStatus = model.SessionStatus
type Session = model.Session
type FileID = model.FileID
type FileStatus = model.FileStatus
type File = model.File
type FileView = model.FileView
type SessionView = model.SessionView
type IssueView = model.IssueView
type IssueSeverity = model.IssueSeverity
type ParseIssue = model.ParseIssue
type ParsedFile = model.ParsedFile
type ParsedRow = model.ParsedRow
type ParsedVariant = model.ParsedVariant
type ParsedDimensions = model.ParsedDimensions
type ParsedSize = model.ParsedSize
type RawCharacteristic = model.RawCharacteristic
type ParsedMedia = model.ParsedMedia
type AggregatedCard = model.AggregatedCard
type AggregatedFile = model.AggregatedFile
type BatchID = model.BatchID
type BatchItemID = model.BatchItemID
type Digest = model.Digest
type SourceGroupKey = model.SourceGroupKey
type BatchCursor = model.BatchCursor
type TrustedActor = model.TrustedActor
type AuthorSnapshot = model.AuthorSnapshot
type BatchHeader = model.BatchHeader
type BatchItem = model.BatchItem

const (
	MaxFilesPerSession = model.MaxFilesPerSession
	MaxFileSize        = model.MaxFileSize

	PurposeTransfer = model.PurposeTransfer
	PurposeEdit     = model.PurposeEdit

	SessionStatusCollecting = model.SessionStatusCollecting
	SessionStatusFinalized  = model.SessionStatusFinalized
	SessionStatusCancelled  = model.SessionStatusCancelled

	FileStatusReserved  = model.FileStatusReserved
	FileStatusStored    = model.FileStatusStored
	FileStatusParsing   = model.FileStatusParsing
	FileStatusValid     = model.FileStatusValid
	FileStatusInvalid   = model.FileStatusInvalid
	FileStatusAbandoned = model.FileStatusAbandoned

	IssueSeverityError   = model.IssueSeverityError
	IssueSeverityWarning = model.IssueSeverityWarning

	BatchSchemaVersion        = model.BatchSchemaVersion
	BatchNormalizationVersion = model.BatchNormalizationVersion
	MaxBatchPageSize          = model.MaxBatchPageSize
)

func NewSourceGroupKey(fileID FileID, group, vendorCode string) SourceGroupKey {
	return model.NewSourceGroupKey(fileID, group, vendorCode)
}

func BatchChecksum(
	purpose Purpose,
	groupsCount int,
	items []BatchItem,
) (Digest, error) {
	return model.BatchChecksum(purpose, groupsCount, items)
}
