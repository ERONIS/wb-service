package transfer_service

import (
	"context"
	"errors"
	"testing"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
)

type automaticAuthorizationPlanSourceStub struct {
	plans     []AuthorizationPlanSummary
	listCalls int
	listErr   error
}

func (stub *automaticAuthorizationPlanSourceStub) ListAutomaticAuthorizationPlans(
	context.Context,
	int,
) ([]AuthorizationPlanSummary, error) {
	stub.listCalls++
	return stub.plans, stub.listErr
}

func (*automaticAuthorizationPlanSourceStub) ListAuthorizationPlans(
	context.Context,
	int64,
	int,
) ([]AuthorizationPlanSummary, error) {
	return nil, errors.New("unexpected ListAuthorizationPlans call")
}

func (*automaticAuthorizationPlanSourceStub) GetAuthorizationPlan(
	context.Context,
	int64,
	TransferID,
) (AuthorizationPlanSummary, error) {
	return AuthorizationPlanSummary{}, errors.New("unexpected GetAuthorizationPlan call")
}

func (*automaticAuthorizationPlanSourceStub) LockAuthorizationPlan(
	context.Context,
	core_postgres_transaction.DBTX,
	int64,
	TransferID,
	Digest,
) (AuthorizationPlanSummary, error) {
	return AuthorizationPlanSummary{}, errors.New("unexpected LockAuthorizationPlan call")
}

type automaticLiveAuthorizationStub struct {
	live         bool
	actors       []LiveTrustedActor
	commands     []RequestLiveCommand
	authorizeErr error
}

func (stub *automaticLiveAuthorizationStub) LiveEnabled() bool {
	return stub.live
}

func (stub *automaticLiveAuthorizationStub) Authorize(
	_ context.Context,
	actor LiveTrustedActor,
	command RequestLiveCommand,
) (LiveAuthorization, error) {
	stub.actors = append(stub.actors, actor)
	stub.commands = append(stub.commands, command)
	return LiveAuthorization{State: LiveAuthorizationAuthorized}, stub.authorizeErr
}

func TestAutomaticAuthorizationProcessorSkipsWhenLiveModeIsDisabled(t *testing.T) {
	plans := &automaticAuthorizationPlanSourceStub{}
	authorization := &automaticLiveAuthorizationStub{live: false}
	processor := NewAutomaticAuthorizationProcessor(plans, authorization)

	if err := processor.ProcessPending(context.Background()); err != nil {
		t.Fatalf("ProcessPending() error = %v", err)
	}
	if plans.listCalls != 0 {
		t.Fatalf("ListAutomaticAuthorizationPlans() calls = %d, want 0", plans.listCalls)
	}
	if len(authorization.commands) != 0 {
		t.Fatalf("Authorize() calls = %d, want 0", len(authorization.commands))
	}
}

func TestAutomaticAuthorizationProcessorAuthorizesExactPlanAsBatchAuthor(t *testing.T) {
	planDigest := Digest{1, 2, 3, 4}
	targetSetRoot := Digest{5, 6, 7, 8}
	plan := AuthorizationPlanSummary{
		TransferID:       41,
		PlanID:           52,
		PlanDigest:       planDigest,
		TargetSetRoot:    targetSetRoot,
		PlanRevision:     3,
		TargetsCount:     1,
		CreateActions:    1,
		AuthorTelegramID: 123456,
	}
	plans := &automaticAuthorizationPlanSourceStub{plans: []AuthorizationPlanSummary{plan}}
	authorization := &automaticLiveAuthorizationStub{live: true}
	processor := NewAutomaticAuthorizationProcessor(plans, authorization)

	if err := processor.ProcessPending(context.Background()); err != nil {
		t.Fatalf("ProcessPending() error = %v", err)
	}
	if plans.listCalls != 1 {
		t.Fatalf("ListAutomaticAuthorizationPlans() calls = %d, want 1", plans.listCalls)
	}
	if len(authorization.actors) != 1 || len(authorization.commands) != 1 {
		t.Fatalf(
			"authorization calls: actors=%d commands=%d, want 1/1",
			len(authorization.actors),
			len(authorization.commands),
		)
	}
	if got := authorization.actors[0].TelegramUserID; got != 123456 {
		t.Fatalf("actor TelegramUserID = %d, want 123456", got)
	}
	command := authorization.commands[0]
	if command.TransferID != plan.TransferID || command.PlanDigest != plan.PlanDigest {
		t.Fatalf("Authorize() command = %#v, want exact transfer and plan digest", command)
	}
	const wantKey = "auto:authorize:41:3:0102030400000000000000000000000000000000000000000000000000000000"
	if command.IdempotencyKey != wantKey {
		t.Fatalf("idempotency key = %q, want %q", command.IdempotencyKey, wantKey)
	}
}
