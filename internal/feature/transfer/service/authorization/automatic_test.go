package authorization

import (
	"context"
	"errors"
	"testing"
)

type authorizationPlanPageCall struct {
	transferID TransferID
	planID     int64
}

type authorizationPlanPageSource struct {
	plans []AuthorizationPlanSummary
	calls []authorizationPlanPageCall
}

type failingAutomaticAuthorization struct {
	attempts []int64
}

func (source *authorizationPlanPageSource) ListAutomaticAuthorizationPlans(
	_ context.Context,
	afterTransferID TransferID,
	afterPlanID int64,
	limit int,
) ([]AuthorizationPlanSummary, error) {
	source.calls = append(source.calls, authorizationPlanPageCall{afterTransferID, afterPlanID})
	page := make([]AuthorizationPlanSummary, 0, limit)
	for _, plan := range source.plans {
		if plan.TransferID < afterTransferID ||
			(plan.TransferID == afterTransferID && plan.PlanID <= afterPlanID) {
			continue
		}
		page = append(page, plan)
		if len(page) == limit {
			break
		}
	}
	return page, nil
}

func (*failingAutomaticAuthorization) LiveEnabled() bool { return true }

func (authorization *failingAutomaticAuthorization) Authorize(
	_ context.Context,
	_ LiveTrustedActor,
	command RequestLiveCommand,
) (LiveAuthorization, error) {
	planID := int64(command.PlanDigest[0])
	authorization.attempts = append(authorization.attempts, planID)
	if planID <= automaticAuthorizationPageSize {
		return LiveAuthorization{}, errors.New("temporary authorization failure")
	}
	return LiveAuthorization{State: LiveAuthorizationAuthorized}, nil
}

func TestAutomaticAuthorizationAdvancesPastFailingPage(t *testing.T) {
	source := &authorizationPlanPageSource{}
	for planID := int64(1); planID <= automaticAuthorizationPageSize+1; planID++ {
		var digest Digest
		digest[0] = byte(planID)
		source.plans = append(source.plans, AuthorizationPlanSummary{
			TransferID:       1,
			PlanID:           planID,
			PlanDigest:       digest,
			TargetSetRoot:    Digest{1},
			TargetsCount:     1,
			AuthorTelegramID: 1,
		})
	}
	authorization := &failingAutomaticAuthorization{}
	processor := NewAutomaticAuthorizationProcessor(source, authorization)

	if err := processor.ProcessPending(context.Background()); err == nil {
		t.Fatal("ожидалась ошибка первой порции")
	}
	if len(authorization.attempts) != automaticAuthorizationPageSize {
		t.Fatalf("ожидалось %d попыток первой порции, получено %d", automaticAuthorizationPageSize, len(authorization.attempts))
	}
	notifications := 0
	if err := processor.ProcessPendingWithNotify(context.Background(), func() {
		notifications++
	}); err != nil {
		t.Fatalf("последний план не обработан: %v", err)
	}
	if notifications != 1 {
		t.Fatalf("ожидалось одно уведомление после авторизации, получено %d", notifications)
	}
	if len(authorization.attempts) != automaticAuthorizationPageSize+1 ||
		authorization.attempts[automaticAuthorizationPageSize] != automaticAuthorizationPageSize+1 {
		t.Fatalf("план за ошибочной порцией не получил попытку: %v", authorization.attempts)
	}
	if len(source.calls) != 2 || source.calls[1] != (authorizationPlanPageCall{1, automaticAuthorizationPageSize}) {
		t.Fatalf("курсор второй порции неверен: %+v", source.calls)
	}
	if err := processor.ProcessPending(context.Background()); err == nil {
		t.Fatal("после конца очереди ожидался повтор первой порции")
	}
	if len(source.calls) != 3 || source.calls[2] != (authorizationPlanPageCall{}) {
		t.Fatalf("курсор не вернулся к началу: %+v", source.calls)
	}
}
