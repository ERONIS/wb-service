package statistics_service

import (
	"errors"
	"strings"
	"time"
)

const (
	DefaultPageSize = 20
	MaxPageSize     = 100
	DetailsPageSize = 100
	CabinetPageSize = 10
)

var (
	ErrInvalidFilter     = errors.New("invalid statistics filter")
	ErrOperationNotFound = errors.New("statistics operation not found")
	ErrActionNotFound    = errors.New("statistics action not found")
)

type OperationFilter struct {
	TransferID        int64
	BatchID           int64
	CabinetID         string
	Phase             string
	Outcome           string
	RequiresAttention *bool
	CreatedFrom       *time.Time
	CreatedTo         *time.Time
	Limit             int
	Offset            int
}

func (filter OperationFilter) Normalize() (OperationFilter, error) {
	filter.CabinetID = strings.TrimSpace(filter.CabinetID)
	filter.Phase = strings.TrimSpace(filter.Phase)
	filter.Outcome = strings.TrimSpace(filter.Outcome)
	if filter.TransferID < 0 || filter.BatchID < 0 || filter.Offset < 0 || filter.Limit < 0 ||
		filter.Limit > MaxPageSize || len(filter.CabinetID) > 128 ||
		!oneOf(filter.Phase, transferPhases...) ||
		!oneOf(filter.Outcome, transferOutcomes...) ||
		invalidRange(filter.CreatedFrom, filter.CreatedTo) {
		return OperationFilter{}, ErrInvalidFilter
	}
	if filter.Limit == 0 {
		filter.Limit = DefaultPageSize
	}
	filter.CreatedFrom = utcTime(filter.CreatedFrom)
	filter.CreatedTo = utcTime(filter.CreatedTo)
	return filter, nil
}

type AggregateFilter struct {
	TransferID          int64
	CabinetID           string
	ActionKind          string
	Phase               string
	OutcomeClass        string
	OutcomeCode         string
	DeliveryState       string
	ResponseDisposition string
	RequiresAttention   *bool
	CreatedFrom         *time.Time
	CreatedTo           *time.Time
}

func (filter AggregateFilter) Normalize() (AggregateFilter, error) {
	filter.CabinetID = strings.TrimSpace(filter.CabinetID)
	filter.ActionKind = strings.TrimSpace(filter.ActionKind)
	filter.Phase = strings.TrimSpace(filter.Phase)
	filter.OutcomeClass = strings.TrimSpace(filter.OutcomeClass)
	filter.OutcomeCode = strings.TrimSpace(filter.OutcomeCode)
	filter.DeliveryState = strings.TrimSpace(filter.DeliveryState)
	filter.ResponseDisposition = strings.TrimSpace(filter.ResponseDisposition)
	if filter.TransferID < 0 || len(filter.CabinetID) > 128 ||
		len(filter.OutcomeCode) > 128 ||
		!oneOf(filter.ActionKind, actionKinds...) ||
		!oneOf(filter.Phase, transferPhases...) ||
		!oneOf(filter.OutcomeClass, outcomeClasses...) ||
		!oneOf(filter.DeliveryState, deliveryStates...) ||
		!oneOf(filter.ResponseDisposition, responseDispositions...) ||
		invalidRange(filter.CreatedFrom, filter.CreatedTo) {
		return AggregateFilter{}, ErrInvalidFilter
	}
	filter.CreatedFrom = utcTime(filter.CreatedFrom)
	filter.CreatedTo = utcTime(filter.CreatedTo)
	return filter, nil
}

type AttentionFilter struct {
	TransferID   int64
	ItemTargetID int64
	Limit        int
	Offset       int
}

type CabinetTaskFilter struct {
	TransferID int64
	TargetID   int64
	Limit      int
	Offset     int
}

func (filter CabinetTaskFilter) Normalize() (CabinetTaskFilter, error) {
	if filter.TransferID <= 0 || filter.TargetID <= 0 || filter.Offset < 0 ||
		filter.Limit < 0 || filter.Limit > MaxPageSize {
		return CabinetTaskFilter{}, ErrInvalidFilter
	}
	if filter.Limit == 0 {
		filter.Limit = CabinetPageSize
	}
	return filter, nil
}

func (filter AttentionFilter) Normalize() (AttentionFilter, error) {
	if filter.TransferID < 0 || filter.ItemTargetID < 0 || filter.Offset < 0 ||
		filter.Limit < 0 || filter.Limit > MaxPageSize {
		return AttentionFilter{}, ErrInvalidFilter
	}
	if filter.Limit == 0 {
		filter.Limit = DefaultPageSize
	}
	return filter, nil
}

func invalidRange(from, to *time.Time) bool {
	return from != nil && to != nil && !from.Before(*to)
}

func utcTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := value.UTC()
	return &normalized
}

var (
	transferPhases = []string{
		"initializing", "preparing", "awaiting_authorization", "publishing",
		"reconciling", "media", "finished",
	}
	transferOutcomes = []string{
		"running", "succeeded", "partial", "rejected", "unresolved", "failed", "cancelled",
	}
	actionKinds    = []string{"create_group", "add_to_group", "upload_media"}
	outcomeClasses = []string{
		"success", "skipped", "rejected", "partial", "unresolved", "internal_error",
	}
	deliveryStates       = []string{"not_dispatched", "response_received", "unknown_delivery"}
	responseDispositions = []string{"accepted", "rejected_proven", "uncertain"}
)

func oneOf(value string, allowed ...string) bool {
	if value == "" {
		return true
	}
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

type OperationRow struct {
	TransferID        int64
	BatchID           int64
	SessionID         int64
	Phase             string
	Outcome           string
	AttentionCode     string
	CohortName        string
	ItemsCount        int
	GroupsCount       int
	TargetsCount      int
	ItemTargetsCount  int64
	GroupTargetsCount int64
	CreatedAt         time.Time
	StartedAt         time.Time
	FinishedAt        *time.Time
}

func (row OperationRow) Duration(now time.Time) time.Duration {
	end := now.UTC()
	if row.FinishedAt != nil {
		end = row.FinishedAt.UTC()
	}
	if end.Before(row.StartedAt) {
		return 0
	}
	return end.Sub(row.StartedAt.UTC())
}

type GroupTargetRow struct {
	GroupTargetID     int64
	SourceGroupID     int64
	GroupName         string
	TargetID          int64
	TargetPosition    int
	CabinetID         string
	PreparationStatus string
	PublicationStatus string
	MediaStatus       string
	OverallOutcome    string
	AttentionCode     string
	ItemsTotal        int64
	ItemsTerminal     int64
	ItemsAttention    int64
	StartedAt         *time.Time
	FinishedAt        *time.Time
}

type ItemRow struct {
	TransferID        int64
	ItemTargetID      int64
	GroupTargetID     int64
	TransferItemID    int64
	TargetID          int64
	TargetPosition    int
	CabinetID         string
	ItemPosition      int
	VendorCode        string
	State             string
	OutcomeClass      string
	OutcomeCode       string
	NMID              int64
	SourceActionID    int64
	AttentionClosedAt *time.Time
	CreatedAt         time.Time
	StartedAt         *time.Time
	FinishedAt        *time.Time
}

type CabinetProgressRow struct {
	TargetID       int64
	TargetPosition int
	CabinetID      string
	Total          int64
	Pending        int64
	Running        int64
	Terminal       int64
	Ready          int64
	Errors         int64
	Attention      int64
}

type CabinetTaskRow struct {
	TransferID        int64
	ItemTargetID      int64
	TargetID          int64
	CabinetID         string
	ItemPosition      int
	VendorCode        string
	GroupName         string
	State             string
	OutcomeClass      string
	OutcomeCode       string
	NMID              int64
	PreparationStatus string
	PublicationStatus string
	MediaStatus       string
	OverallOutcome    string
	AttentionCode     string
	MediaActionState  string
	MediaOutcomeCode  string
	CreatedAt         time.Time
	StartedAt         *time.Time
	FinishedAt        *time.Time
}

type CabinetTaskPage struct {
	Tasks []CabinetTaskRow
	Total int64
}

type ActionRow struct {
	TransferID      int64
	ActionID        int64
	PlanID          int64
	TargetID        int64
	TargetPosition  int
	CabinetID       string
	Kind            string
	State           string
	Revision        int64
	OutcomeClass    string
	OutcomeCode     string
	AuthorizationID int64
	MembersCount    int64
	CreatedAt       time.Time
	StartedAt       *time.Time
	FinishedAt      *time.Time
}

type AttemptRow struct {
	TransferID              int64
	AttemptID               int64
	ActionID                int64
	AuthorizationID         int64
	RecheckObservationID    int64
	AttributionID           int64
	PlanID                  int64
	TargetID                int64
	TargetPosition          int
	CabinetID               string
	ActionKind              string
	DeliveryState           string
	HTTPStatus              int
	ResponseDisposition     string
	ClassifierVersion       int
	SafeErrorCode           string
	UnmatchedCount          int
	ErrorBaselineCabinetID  string
	ErrorCursorRevision     int64
	ErrorCursorUpdatedAt    *time.Time
	ErrorCursorBatchUUID    string
	ErrorBaselineCapturedAt time.Time
	StartedAt               time.Time
	FinishedAt              *time.Time
}

type AuthorizationRow struct {
	TransferID      int64
	AuthorizationID int64
	PlanID          int64
	Revision        int64
	State           string
	RequestedAt     time.Time
	ApprovedAt      *time.Time
	ExpiresAt       time.Time
	RevokedAt       *time.Time
	ClosedAt        *time.Time
	SafeReasonCode  string
	CreatedAt       time.Time
}

type OperationDetails struct {
	Operation               OperationRow
	Groups                  []GroupTargetRow
	Items                   []ItemRow
	Actions                 []ActionRow
	Attempts                []AttemptRow
	Authorizations          []AuthorizationRow
	GroupsTruncated         bool
	ItemsTruncated          bool
	ActionsTruncated        bool
	AttemptsTruncated       bool
	AuthorizationsTruncated bool
}

type ActionDetails struct {
	Action  ActionRow
	Attempt *AttemptRow
}

type TransferTotals struct {
	Total             int64
	Terminal          int64
	Succeeded         int64
	Partial           int64
	Rejected          int64
	Unresolved        int64
	Failed            int64
	Cancelled         int64
	RequiresAttention int64
	AverageDuration   time.Duration
}

type ItemTotals struct {
	Total             int64
	Terminal          int64
	Success           int64
	Skipped           int64
	Rejected          int64
	Partial           int64
	Unresolved        int64
	InternalError     int64
	RequiresAttention int64
	AverageDuration   time.Duration
}

type ActionTotals struct {
	Total            int64
	Terminal         int64
	Success          int64
	Skipped          int64
	Rejected         int64
	Partial          int64
	Unresolved       int64
	InternalError    int64
	Attempts         int64
	RealWBRoundTrips int64
	UnknownDelivery  int64
	AverageDuration  time.Duration
}

type ErrorGroup struct {
	Source       string
	OutcomeClass string
	OutcomeCode  string
	Count        int64
}

type AttentionRow struct {
	TransferID            int64
	ItemTargetID          int64
	GroupTargetID         int64
	CabinetID             string
	VendorCode            string
	OutcomeClass          string
	OutcomeCode           string
	ActionID              int64
	ActionMemberID        int64
	ActionRevision        int64
	ActionKind            string
	AttemptID             int64
	ObservationEvidenceID int64
	ErrorBatchEvidenceID  int64
	FinishedAt            time.Time
}
