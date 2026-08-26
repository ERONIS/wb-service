package statistics_service

import "context"

type Reader interface {
	ListOperations(context.Context, OperationFilter) ([]OperationRow, error)
	GetOperation(context.Context, int64) (OperationDetails, error)
	GetAction(context.Context, int64) (ActionDetails, error)
	ListCabinetProgress(context.Context, int64) ([]CabinetProgressRow, error)
	ListCabinetTasks(context.Context, CabinetTaskFilter) (CabinetTaskPage, error)
	AggregateTransfers(context.Context, AggregateFilter) (TransferTotals, error)
	AggregateItems(context.Context, AggregateFilter) (ItemTotals, error)
	AggregateActions(context.Context, AggregateFilter) (ActionTotals, error)
	AggregateErrors(context.Context, AggregateFilter) ([]ErrorGroup, error)
	ListAttention(context.Context, AttentionFilter) ([]AttentionRow, error)
}
