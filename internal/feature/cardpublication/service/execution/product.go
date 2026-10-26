package execution

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	cardpublication_catalog "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/catalog"
	cardpublication_model "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/model"
	cardpublication_monitoring "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/monitoring"
	transfer_authorization "github.com/ERONIS/wb-service/internal/feature/transfer/service/authorization"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
)

const submissionClassifierVersion = 1

var (
	ErrProductActionConflict = fmt.Errorf(
		"publication product action changed: %w",
		core_errors.ErrConflict,
	)
	ErrProductPlanSuperseded = fmt.Errorf(
		"publication plan superseded before dispatch: %w",
		core_errors.ErrConflict,
	)
	ErrProductPlanStopped = fmt.Errorf(
		"publication plan stopped after dispatch began: %w",
		core_errors.ErrConflict,
	)
)

type ProductActionCandidate struct {
	TransferID            transfer_model.TransferID
	PlanID                int64
	ActionID              int64
	TargetID              int64
	SellerID              domain.SellerID
	AuthorizationID       transfer_authorization.LiveAuthorizationID
	AuthorizationRevision int64
	CatalogVendorCodes    []string
}

type ProductActionMember struct {
	ID                   int64
	GroupTargetID        int64
	TransferItemTargetID int64
	RequestMemberIndex   int
	VendorCode           string
	OutcomeClass         transfer_model.ResultClass
	OutcomeCode          string
	NMID                 int64
}

type AbortedProductAction struct {
	ActionID int64
	Members  []ProductActionMember
}

type ProductAction struct {
	ProductActionCandidate
	Revision               int64
	Kind                   cardpublication_model.ActionKind
	State                  string
	RequestPayload         []byte
	SellerID               domain.SellerID
	ClientGeneration       domain.ClientGeneration
	CredentialExpiresAt    time.Time
	PreflightObservationID int64
	Members                []ProductActionMember
	AttemptID              int64
	RecheckObservationID   int64
	AttemptStartedAt       time.Time
	AttemptFinishedAt      time.Time
	AttemptDelivery        SubmissionDelivery
	AttemptHTTPStatus      int
	AttemptSafeCode        string
	EmptyResponseRetry     ProductRetry
}

type TargetedRecheck struct {
	Observation cardpublication_model.ObservationDraft
	Stable      bool
	SafeCode    string
}

type ProductAttempt struct {
	ID                   int64
	TransferID           transfer_model.TransferID
	ActionID             int64
	AuthorizationID      transfer_authorization.LiveAuthorizationID
	RecheckObservationID int64
	StartedAt            time.Time
}

type ProductRetry struct {
	ID                int64
	TransferID        transfer_model.TransferID
	ActionID          int64
	AuthorizationID   transfer_authorization.LiveAuthorizationID
	RequestPayload    []byte
	StartedAt         time.Time
	FinishedAt        time.Time
	Delivery          SubmissionDelivery
	HTTPStatus        int
	Disposition       SubmissionDisposition
	ClassifierVersion int
	SafeCode          string
	UnmatchedCount    int
}

type SubmissionDelivery string

const (
	SubmissionNotDispatched    SubmissionDelivery = "not_dispatched"
	SubmissionResponseReceived SubmissionDelivery = "response_received"
	SubmissionUnknownDelivery  SubmissionDelivery = "unknown_delivery"
)

type SubmissionDisposition string

const (
	SubmissionAccepted       SubmissionDisposition = "accepted"
	SubmissionRejectedProven SubmissionDisposition = "rejected_proven"
	SubmissionUncertain      SubmissionDisposition = "uncertain"
)

type MemberSubmissionResult struct {
	ActionMemberID int64
	OutcomeClass   transfer_model.ResultClass
	OutcomeCode    string
}

type SubmissionResult struct {
	Delivery          SubmissionDelivery
	HTTPStatus        int
	ClassifierVersion int
	Disposition       SubmissionDisposition
	SafeCode          string
	MemberResults     []MemberSubmissionResult
	UnmatchedCount    int
}

type BeginProductAttemptCommand struct {
	Action               ProductAction
	Authorization        transfer_authorization.LiveAuthorizationEvidence
	Baseline             cardpublication_monitoring.ErrorBaseline
	RecheckObservationID int64
}

type RecordSubmissionCommand struct {
	Action  ProductAction
	Attempt ProductAttempt
	Result  SubmissionResult
}

type BeginProductRetryCommand struct {
	Action ProductAction
}

type RecordProductRetryCommand struct {
	Action ProductAction
	Retry  ProductRetry
	Result SubmissionResult
}

type ErrorBatchMatch struct {
	ID                  int64
	BatchUUID           string
	UpdatedAt           time.Time
	RejectedVendorCodes []string
	ErrorCodes          []string
}

type MemberReconciliationResult struct {
	ActionMemberID   int64
	OutcomeClass     transfer_model.ResultClass
	OutcomeCode      string
	NMID             int64
	IMTID            int64
	SubjectID        int64
	ErrorBatchID     int64
	AttributionLevel string
}

type ProductJournalRepository interface {
	ListDispatchableProductActions(
		ctx context.Context,
		afterActionID int64,
		limit int,
	) ([]ProductActionCandidate, error)

	ListInterruptedProductActions(
		ctx context.Context,
		afterActionID int64,
		limit int,
	) ([]ProductActionCandidate, error)

	LockProductAction(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		candidate ProductActionCandidate,
	) (ProductAction, error)

	InsertActionObservation(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		transferID transfer_model.TransferID,
		kind string,
		observation cardpublication_model.ObservationDraft,
	) (int64, error)

	PlanAttemptCount(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		transferID transfer_model.TransferID,
		planID int64,
	) (int, error)

	SupersedePlanBeforeDispatch(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		action ProductAction,
		safeCode string,
	) error

	AbortPlanAfterDispatch(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		action ProductAction,
		safeCode string,
	) ([]AbortedProductAction, error)

	BeginProductAttempt(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		command BeginProductAttemptCommand,
	) (ProductAttempt, error)

	RecordSubmission(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		command RecordSubmissionCommand,
	) error

	BeginProductRetry(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		command BeginProductRetryCommand,
	) (ProductRetry, error)

	RecordProductRetry(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		command RecordProductRetryCommand,
	) error

	ListReconcilingProductActions(
		ctx context.Context,
		now time.Time,
		baseDelay time.Duration,
		afterActionID int64,
		limit int,
	) ([]ProductActionCandidate, error)

	LoadErrorBatchMatches(
		ctx context.Context,
		action ProductAction,
	) ([]ErrorBatchMatch, error)

	RecordProductReconciliationPoll(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		action ProductAction,
		postObservationID int64,
		memberResults []MemberReconciliationResult,
	) error

	PublicationPlanTerminal(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		transferID transfer_model.TransferID,
		planID int64,
	) (bool, error)

	FinishProductReconciliation(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		action ProductAction,
		postObservationID int64,
		memberResults []MemberReconciliationResult,
		actionClass transfer_model.ResultClass,
		actionCode string,
	) error

	PendingMediaGroupTargetIDs(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		action ProductAction,
	) ([]int64, error)
}

type LiveAuthorizationVerifier interface {
	LockValid(
		context.Context,
		core_postgres_transaction.DBTX,
		transfer_authorization.LiveAuthorizationCheck,
	) (transfer_authorization.LiveAuthorizationEvidence, error)
	LockValidProductRetry(
		context.Context,
		core_postgres_transaction.DBTX,
		transfer_authorization.LiveAuthorizationCheck,
	) (transfer_authorization.LiveAuthorizationEvidence, error)
	SupersedeWithin(
		context.Context,
		core_postgres_transaction.DBTX,
		transfer_authorization.ChangeLiveAuthorizationStateCommand,
	) (transfer_authorization.LiveAuthorization, error)
	CloseWithin(
		context.Context,
		core_postgres_transaction.DBTX,
		transfer_authorization.ChangeLiveAuthorizationStateCommand,
	) (transfer_authorization.LiveAuthorization, error)
}

func (action ProductAction) Validate() error {
	if action.TransferID <= 0 || action.ActionID <= 0 || action.TargetID <= 0 ||
		action.SellerID.Validate() != nil || action.AuthorizationID <= 0 ||
		action.AuthorizationRevision < 0 || action.PlanID <= 0 ||
		action.Revision < 0 || len(action.RequestPayload) == 0 ||
		action.ClientGeneration.IsZero() ||
		action.CredentialExpiresAt.IsZero() || action.PreflightObservationID <= 0 ||
		len(action.Members) == 0 {
		return errors.New("publication product action is invalid")
	}
	if action.Kind != cardpublication_model.ActionCreateGroup && action.Kind != cardpublication_model.ActionAddToGroup {
		return errors.New("publication action is not a product mutation")
	}
	for index, member := range action.Members {
		if member.ID <= 0 || member.GroupTargetID <= 0 ||
			member.TransferItemTargetID <= 0 || member.RequestMemberIndex != index ||
			member.VendorCode == "" || strings.TrimSpace(member.VendorCode) != member.VendorCode {
			return fmt.Errorf("publication product member at index %d is invalid", index)
		}
		if member.OutcomeClass == "" {
			if member.OutcomeCode != "" || member.NMID != 0 {
				return fmt.Errorf("pending publication product member at index %d has a result", index)
			}
			continue
		}
		if !member.OutcomeClass.IsValid() || member.OutcomeCode == "" ||
			strings.TrimSpace(member.OutcomeCode) != member.OutcomeCode ||
			len(member.OutcomeCode) > 128 || member.NMID < 0 ||
			(member.OutcomeClass == transfer_model.ResultSuccess && member.NMID <= 0) {
			return fmt.Errorf("terminal publication product member at index %d is invalid", index)
		}
	}
	requestVendorCodes := make([]string, 0, len(action.Members))
	switch action.Kind {
	case cardpublication_model.ActionCreateGroup:
		var request contentapi.UploadCardsRequest
		if err := cardpublication_model.DecodeExactJSON(action.RequestPayload, &request); err != nil {
			return errors.New("publication create request is invalid")
		}
		for _, group := range request {
			for _, variant := range group.Variants {
				requestVendorCodes = append(requestVendorCodes, variant.VendorCode)
			}
		}
	case cardpublication_model.ActionAddToGroup:
		var request contentapi.UploadCardsAddRequest
		if err := cardpublication_model.DecodeExactJSON(action.RequestPayload, &request); err != nil || request.IMTID <= 0 {
			return errors.New("publication add request is invalid")
		}
		for _, variant := range request.CardsToAdd {
			requestVendorCodes = append(requestVendorCodes, variant.VendorCode)
		}
	}
	if len(requestVendorCodes) != len(action.Members) {
		return errors.New("publication product request member count differs")
	}
	for index, vendorCode := range requestVendorCodes {
		if vendorCode != action.Members[index].VendorCode {
			return errors.New("publication product request member order differs")
		}
	}
	return nil
}

func (action ProductAction) GroupTargetIDs() []int64 {
	set := make(map[int64]struct{})
	for _, member := range action.Members {
		set[member.GroupTargetID] = struct{}{}
	}
	result := make([]int64, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool { return result[left] < result[right] })
	return result
}

func (action ProductAction) VendorCodes() []string {
	result := make([]string, len(action.Members))
	for index, member := range action.Members {
		result[index] = member.VendorCode
	}
	return result
}

func (action ProductAction) PendingMembers() []ProductActionMember {
	result := make([]ProductActionMember, 0, len(action.Members))
	for _, member := range action.Members {
		if member.OutcomeClass == "" {
			result = append(result, member)
		}
	}
	return result
}

func (action ProductAction) PendingVendorCodes() []string {
	members := action.PendingMembers()
	result := make([]string, len(members))
	for index, member := range members {
		result[index] = member.VendorCode
	}
	return result
}

func BuildTargetedRecheck(
	action ProductAction,
	observation cardpublication_catalog.CatalogObservation,
) (TargetedRecheck, error) {
	if err := action.Validate(); err != nil {
		return TargetedRecheck{}, err
	}
	if observation.TargetID != action.TargetID || observation.SellerID != action.SellerID {
		return TargetedRecheck{}, ErrProductActionConflict
	}
	relevantNormal := make([]contentapi.Card, 0)
	relevantTrash := make([]contentapi.TrashCard, 0)
	vendors := make(map[string]struct{}, len(action.Members))
	for _, member := range action.Members {
		vendors[member.VendorCode] = struct{}{}
	}
	var addRequest contentapi.UploadCardsAddRequest
	if action.Kind == cardpublication_model.ActionAddToGroup {
		if err := cardpublication_model.DecodeExactJSON(action.RequestPayload, &addRequest); err != nil || addRequest.IMTID <= 0 {
			return TargetedRecheck{}, ErrProductActionConflict
		}
	}
	for _, card := range observation.Normal {
		_, relevantVendor := vendors[card.VendorCode]
		if relevantVendor || (action.Kind == cardpublication_model.ActionAddToGroup && card.IMTID == addRequest.IMTID) {
			relevantNormal = append(relevantNormal, card)
		}
	}
	for _, card := range observation.Trash {
		if _, relevant := vendors[card.VendorCode]; relevant {
			relevantTrash = append(relevantTrash, card)
		}
	}
	targeted := cardpublication_catalog.CatalogObservation{
		TargetID:   observation.TargetID,
		SellerID:   observation.SellerID,
		Normal:     relevantNormal,
		Trash:      relevantTrash,
		ObservedAt: observation.ObservedAt,
	}
	draft, err := cardpublication_catalog.DraftObservation(targeted)
	if err != nil {
		return TargetedRecheck{}, err
	}
	stable, safeCode := productDecisionStillStable(action, relevantNormal, relevantTrash, addRequest)
	return TargetedRecheck{Observation: draft, Stable: stable, SafeCode: safeCode}, nil
}

func productDecisionStillStable(
	action ProductAction,
	normal []contentapi.Card,
	trash []contentapi.TrashCard,
	_ contentapi.UploadCardsAddRequest,
) (bool, string) {
	for _, member := range action.Members {
		normalCount, trashCount := 0, 0
		for _, card := range normal {
			if card.VendorCode == member.VendorCode {
				normalCount++
			}
		}
		for _, card := range trash {
			if card.VendorCode == member.VendorCode {
				trashCount++
			}
		}
		if normalCount != 0 || trashCount != 0 {
			return false, "TARGETED_RECHECK_REMOTE_CHANGED"
		}
	}
	return true, "TARGETED_RECHECK_STABLE"
}
