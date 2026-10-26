package transfer_postgres_repository_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_postgres_repository "github.com/ERONIS/wb-service/internal/feature/cardpublication/repository/postgres"
	transfer_postgres_repository "github.com/ERONIS/wb-service/internal/feature/transfer/repository/postgres"
	transfer_authorization "github.com/ERONIS/wb-service/internal/feature/transfer/service/authorization"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
)

type retryAuthorizationFixture struct {
	check         transfer_authorization.LiveAuthorizationCheck
	seller        domain.SellerID
	ownerID       int64
	groupTargetID int64
	itemTargetID  int64
}

func setupRetryAuthorizationFixture(
	t *testing.T,
	ctx context.Context,
	pool core_postgres_pool.Pool,
) retryAuthorizationFixture {
	t.Helper()
	unique := time.Now().UnixNano()
	seller := domain.SellerID(fmt.Sprintf("00000000-0000-0000-0000-%012x", uint64(unique)&0xffffffffffff))
	ownerID := unique
	var batchID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO wb.card_batches (
			source_session_id, author_telegram_id, finalized_from_revision,
			finalize_idempotency_key, items_count, groups_count, sealed
		) VALUES ($1, $1, 0, 'retry-test', 1, 1, TRUE)
		RETURNING id;
	`, unique).Scan(&batchID); err != nil {
		t.Fatalf("создать пачку: %v", err)
	}
	var transferID transfer_model.TransferID
	if err := pool.QueryRow(ctx, `
		INSERT INTO wb.transfers (
			batch_id, phase, outcome, items_count, groups_count,
			targets_count, item_targets_count, group_targets_count
		) VALUES ($1, 'initializing', 'running', 1, 1, 1, 1, 1)
		RETURNING id;
	`, batchID).Scan(&transferID); err != nil {
		t.Fatalf("создать перенос: %v", err)
	}
	generation := domain.ClientGeneration(1)
	var targetID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO wb.transfer_targets (
			transfer_id, position, seller_id, client_generation,
			credential_expires_at, binding_revision, capability_revision,
			content_read, content_write
		) VALUES (
			$1, 1, $2, $3, CURRENT_TIMESTAMP + INTERVAL '1 day',
			1, 1, TRUE, TRUE
		) RETURNING id;
	`, transferID, seller, int64(generation)).Scan(&targetID); err != nil {
		t.Fatalf("создать цель переноса: %v", err)
	}
	var groupID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO wb.transfer_groups (transfer_id, source_group_number, source_group_name)
		VALUES ($1, 1, 'Тестовая группа') RETURNING id;
	`, transferID).Scan(&groupID); err != nil {
		t.Fatalf("создать группу: %v", err)
	}
	var itemID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO wb.transfer_items (
			transfer_id, batch_item_id, position, source_group_id, vendor_code, payload
		) VALUES ($1, 1, 1, $2, 'ART-1', '{}'::jsonb) RETURNING id;
	`, transferID, groupID).Scan(&itemID); err != nil {
		t.Fatalf("создать карточку переноса: %v", err)
	}
	var groupTargetID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO wb.transfer_group_targets (transfer_id, source_group_id, target_id)
		VALUES ($1, $2, $3) RETURNING id;
	`, transferID, groupID, targetID).Scan(&groupTargetID); err != nil {
		t.Fatalf("создать цель группы: %v", err)
	}
	var itemTargetID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO wb.transfer_item_targets (
			transfer_id, source_group_id, transfer_item_id, group_target_id
		) VALUES ($1, $2, $3, $4) RETURNING id;
	`, transferID, groupID, itemID, groupTargetID).Scan(&itemTargetID); err != nil {
		t.Fatalf("создать цель карточки: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE wb.transfers SET phase = 'active', updated_at = CURRENT_TIMESTAMP
		WHERE id = $1;
	`, transferID); err != nil {
		t.Fatalf("активировать перенос: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO wb.users (tg_id, full_name, role)
		VALUES ($1, 'Тестовый владелец', 'partner');
	`, ownerID); err != nil {
		t.Fatalf("создать владельца кабинета: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO wb.api_cabinets (
			seller_id, owner_tg_id, display_name, normalized_name,
			token, content_read, content_write, client_generation,
			credential_expires_at, verified_at
		) VALUES (
			$1, $2, 'Кабинет теста', 'кабинет теста',
			'test-token', TRUE, TRUE, $3,
			CURRENT_TIMESTAMP + INTERVAL '1 day', CURRENT_TIMESTAMP
		);
	`, seller, ownerID, int64(generation)); err != nil {
		t.Fatalf("создать кабинет: %v", err)
	}
	var planID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO wb.publication_plans (transfer_id, state, started_at)
		VALUES ($1, 'executing', CURRENT_TIMESTAMP)
		RETURNING id;
	`, transferID).Scan(&planID); err != nil {
		t.Fatalf("создать план: %v", err)
	}
	var authorizationID transfer_authorization.LiveAuthorizationID
	if err := pool.QueryRow(ctx, `
		INSERT INTO wb.transfer_live_authorizations (
			transfer_id, plan_id, state, trusted_actor_id,
			trusted_actor_name, requested_at, approved_at, expires_at
		) VALUES (
			$1, $2, 'authorized', $3,
			'Тестовый владелец', CURRENT_TIMESTAMP - INTERVAL '1 hour',
			CURRENT_TIMESTAMP - INTERVAL '1 hour',
			CURRENT_TIMESTAMP + INTERVAL '1 hour'
		) RETURNING id;
	`, transferID, planID, ownerID).Scan(&authorizationID); err != nil {
		t.Fatalf("создать авторизацию: %v", err)
	}
	var actionID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO wb.publication_actions (
			transfer_id, plan_id, target_id, position, kind,
			state, request_payload, authorization_id, started_at
		) VALUES (
			$1, $2, $3, 1, 'create_group',
			'reconciling', $4, $5, CURRENT_TIMESTAMP
		) RETURNING id;
	`, transferID, planID, targetID, []byte(`{"cards":[]}`), authorizationID).Scan(&actionID); err != nil {
		t.Fatalf("создать действие: %v", err)
	}
	return retryAuthorizationFixture{
		check: transfer_authorization.LiveAuthorizationCheck{
			TransferID:                    transferID,
			ActionID:                      actionID,
			AuthorizationID:               authorizationID,
			ExpectedAuthorizationRevision: 0,
			PlanID:                        planID,
			TargetID:                      targetID,
			SellerID:                      seller,
			ClientGeneration:              generation,
		},
		seller:        seller,
		ownerID:       ownerID,
		groupTargetID: groupTargetID,
		itemTargetID:  itemTargetID,
	}
}

func TestPostgres_ProductRetryRequiresCurrentLiveAuthorization(t *testing.T) {
	tests := []struct {
		name      string
		change    string
		nowOffset time.Duration
		wantErr   error
		wantState string
	}{
		{
			name:      "истёк срок",
			nowOffset: 2 * time.Hour,
			wantErr:   transfer_authorization.ErrLiveExpired,
			wantState: "expired",
		},
		{
			name: "отозван доступ",
			change: `UPDATE wb.transfer_live_authorizations
				SET state = 'revoked', revoked_at = CURRENT_TIMESTAMP,
				    safe_reason_code = 'revoked_by_owner', revision = revision + 1
				WHERE id = $1;`,
			wantErr:   transfer_authorization.ErrLiveAuthorization,
			wantState: "revoked",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool, uow := getTestPool(t)
			defer pool.Close()
			ctx := context.Background()
			fixture := setupRetryAuthorizationFixture(t, ctx, pool)
			repository := transfer_postgres_repository.New(pool)
			if err := uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
				_, err := repository.LockValidProductRetry(ctx, tx, time.Now().UTC(), fixture.check)
				return err
			}); err != nil {
				t.Fatalf("действительная авторизация отклонена: %v", err)
			}
			if test.change != "" {
				if _, err := pool.Exec(ctx, test.change, fixture.check.AuthorizationID); err != nil {
					t.Fatalf("изменить авторизацию: %v", err)
				}
			}
			var verificationErr error
			if err := uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
				_, verificationErr = repository.LockValidProductRetry(ctx, tx, time.Now().UTC().Add(test.nowOffset), fixture.check)
				return nil
			}); err != nil {
				t.Fatalf("завершить проверку: %v", err)
			}
			if !errors.Is(verificationErr, test.wantErr) {
				t.Fatalf("ошибка проверки: %v; ожидалось %v", verificationErr, test.wantErr)
			}
			var state string
			if err := pool.QueryRow(ctx, `
				SELECT state FROM wb.transfer_live_authorizations WHERE id = $1;
			`, fixture.check.AuthorizationID).Scan(&state); err != nil {
				t.Fatalf("прочитать состояние авторизации: %v", err)
			}
			if state != test.wantState {
				t.Fatalf("состояние авторизации: %s; ожидалось %s", state, test.wantState)
			}
		})
	}
}

func TestPostgres_ProductRetryRejectsChangedCabinet(t *testing.T) {
	tests := []struct {
		name     string
		change   string
		identity func(retryAuthorizationFixture) any
	}{
		{
			name: "сменилось поколение токена",
			change: `UPDATE wb.api_cabinets
				SET client_generation = client_generation + 1
				WHERE seller_id = $1;`,
			identity: func(fixture retryAuthorizationFixture) any { return fixture.seller },
		},
		{
			name: "отозвано право записи",
			change: `UPDATE wb.api_cabinets
				SET content_write = FALSE, capability_revision = capability_revision + 1
				WHERE seller_id = $1;`,
			identity: func(fixture retryAuthorizationFixture) any { return fixture.seller },
		},
		{
			name: "кабинет отключён",
			change: `UPDATE wb.api_cabinets
				SET status = 'expired' WHERE seller_id = $1;`,
			identity: func(fixture retryAuthorizationFixture) any { return fixture.seller },
		},
		{
			name:     "владелец утратил роль",
			change:   `UPDATE wb.users SET role = 'user' WHERE tg_id = $1;`,
			identity: func(fixture retryAuthorizationFixture) any { return fixture.ownerID },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool, uow := getTestPool(t)
			defer pool.Close()
			ctx := context.Background()
			fixture := setupRetryAuthorizationFixture(t, ctx, pool)
			if _, err := pool.Exec(ctx, test.change, test.identity(fixture)); err != nil {
				t.Fatalf("изменить кабинет или роль: %v", err)
			}
			repository := transfer_postgres_repository.New(pool)
			err := uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
				_, err := repository.LockValidProductRetry(ctx, tx, time.Now().UTC(), fixture.check)
				return err
			})
			if !errors.Is(err, transfer_authorization.ErrLiveAuthorization) {
				t.Fatalf("изменённый кабинет должен блокировать повтор: %v", err)
			}
		})
	}
}

func TestPostgres_ProductDispatchPageRotatesBeyondFirstHundred(t *testing.T) {
	pool, _ := getTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	fixture := setupRetryAuthorizationFixture(t, ctx, pool)
	if _, err := pool.Exec(ctx, `
		INSERT INTO wb.publication_actions (
			transfer_id, plan_id, target_id, position, kind, state, request_payload
		)
		SELECT $1, $2, $3, position, 'create_group', 'planned', $4
		FROM generate_series(2, 122) AS position;
	`, fixture.check.TransferID, fixture.check.PlanID, fixture.check.TargetID, []byte(`{"cards":[]}`)); err != nil {
		t.Fatalf("создать очередь публикации: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO wb.publication_action_members (
			transfer_id, action_id, target_id, group_target_id,
			transfer_item_target_id, request_member_index, vendor_code
		)
		SELECT action.transfer_id, action.id, action.target_id, $4,
		       $5, 0, 'ART-1'
		FROM wb.publication_actions AS action
		WHERE action.transfer_id = $1 AND action.plan_id = $2
		  AND action.target_id = $3 AND action.state = 'planned';
	`, fixture.check.TransferID, fixture.check.PlanID, fixture.check.TargetID,
		fixture.groupTargetID, fixture.itemTargetID); err != nil {
		t.Fatalf("создать участников действий: %v", err)
	}
	repository := cardpublication_postgres_repository.New(pool)
	first, err := repository.ListDispatchableProductActions(ctx, 0, 100)
	if err != nil || len(first) != 100 {
		t.Fatalf("первая страница: len=%d err=%v", len(first), err)
	}
	second, err := repository.ListDispatchableProductActions(ctx, first[len(first)-1].ActionID, 100)
	if err != nil || len(second) != 100 {
		t.Fatalf("вторая страница: len=%d err=%v", len(second), err)
	}
	seen := make(map[int64]struct{}, 121)
	for _, action := range first {
		seen[action.ActionID] = struct{}{}
	}
	for _, action := range second {
		seen[action.ActionID] = struct{}{}
	}
	if len(seen) != 121 {
		t.Fatalf("после двух страниц обработано %d из 121 действий", len(seen))
	}
}
