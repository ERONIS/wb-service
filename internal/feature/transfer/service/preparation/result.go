package preparation

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	cardprepare_model "github.com/ERONIS/wb-service/internal/feature/cardprepare/service/model"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
)

var ErrPreparationResultConflict = core_errors.ErrConflict

type PreparationResultStatus string

const (
	PreparationResultSucceeded  PreparationResultStatus = "succeeded"
	PreparationResultRejected   PreparationResultStatus = "rejected"
	PreparationResultUnresolved PreparationResultStatus = "unresolved"
)

type PreparationArtifact struct {
	SubjectID        int64
	RequestPayload   []byte
	MetadataSnapshot []byte
	LimitsFree       int64
	LimitsPaid       int64
	LimitsObservedAt time.Time
}

// ApplyPreparationResultCommand содержит параметры применения результата подготовки целевой группы.
type ApplyPreparationResultCommand struct {
	TransferID    transfer_model.TransferID
	GroupTargetID int64
	Status        PreparationResultStatus
	OutcomeCode   string
	ItemPosition  int
	Field         string
	StartedAt     time.Time
	FinishedAt    time.Time
	Artifact      *PreparationArtifact
}

// Validate проверяет согласованность команды применения результата подготовки и её диагностических данных.
func (command ApplyPreparationResultCommand) Validate() error {
	if command.TransferID <= 0 || command.GroupTargetID <= 0 ||
		strings.TrimSpace(command.OutcomeCode) != command.OutcomeCode ||
		command.OutcomeCode == "" || len(command.OutcomeCode) > 128 ||
		command.StartedAt.IsZero() || command.FinishedAt.IsZero() ||
		command.FinishedAt.Before(command.StartedAt) {
		return errors.New("apply transfer preparation result command is invalid")
	}
	if command.ItemPosition < 0 || len(command.Field) > 128 ||
		strings.TrimSpace(command.Field) != command.Field {
		return errors.New("apply transfer preparation result diagnostics are invalid")
	}
	switch command.Status {
	case PreparationResultSucceeded:
		if command.OutcomeCode != "prepared" ||
			command.Artifact == nil || command.ItemPosition != 0 || command.Field != "" {
			return errors.New("successful transfer preparation result is invalid")
		}
		artifact := command.Artifact
		if artifact.SubjectID <= 0 || artifact.LimitsObservedAt.IsZero() ||
			artifact.LimitsFree < 0 || artifact.LimitsPaid < 0 {
			return errors.New("transfer preparation artifact is invalid")
		}
		request, err := validatePreparationRequest(artifact.RequestPayload, artifact.SubjectID)
		if err != nil {
			return err
		}
		var snapshot cardprepare_model.CatalogSnapshot
		if err := json.Unmarshal(artifact.MetadataSnapshot, &snapshot); err != nil {
			return errors.New("transfer preparation metadata snapshot is invalid")
		}
		proposal := cardprepare_model.Proposal{
			SubjectID:        artifact.SubjectID,
			Request:          request,
			EncodedRequest:   artifact.RequestPayload,
			MetadataSnapshot: snapshot,
			Limits: contentapi.CardsLimits{
				FreeLimits: artifact.LimitsFree,
				PaidLimits: artifact.LimitsPaid,
			},
			LimitsObservedAt: artifact.LimitsObservedAt,
		}
		if err := proposal.Validate(); err != nil {
			return errors.New("transfer preparation artifact is invalid")
		}
	case PreparationResultRejected, PreparationResultUnresolved:
		if command.Artifact != nil {
			return errors.New("unsuccessful transfer preparation result has an artifact")
		}
	default:
		return errors.New("transfer preparation result status is invalid")
	}
	return nil
}

func validatePreparationRequest(payload []byte, subjectID int64) (contentapi.UploadCardsRequest, error) {
	var request contentapi.UploadCardsRequest
	if err := json.Unmarshal(payload, &request); err != nil ||
		len(request) != 1 || request[0].SubjectID != subjectID ||
		len(request[0].Variants) == 0 {
		return nil, errors.New("transfer preparation request is invalid")
	}
	seenVendorCodes := make(map[string]struct{}, len(request[0].Variants))
	for _, variant := range request[0].Variants {
		if variant.VendorCode == "" ||
			strings.TrimSpace(variant.VendorCode) != variant.VendorCode {
			return nil, errors.New("transfer preparation request vendor code is invalid")
		}
		if _, exists := seenVendorCodes[variant.VendorCode]; exists {
			return nil, errors.New("transfer preparation request vendor code is duplicated")
		}
		seenVendorCodes[variant.VendorCode] = struct{}{}
	}
	return request, nil
}
