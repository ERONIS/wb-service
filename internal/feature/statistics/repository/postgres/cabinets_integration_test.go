package statistics_postgres_repository

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	service "github.com/ERONIS/wb-service/internal/feature/statistics/service"
	"github.com/jackc/pgx/v5"
)

type cabinetTestPool struct {
	pool.Pool
	tx     pgx.Tx
	schema string
}

func (p cabinetTestPool) OperationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 5*time.Second)
}
func (p cabinetTestPool) Query(ctx context.Context, query string, args ...any) (pgx.Rows, error) {
	return p.tx.Query(ctx, strings.ReplaceAll(query, "wb.", p.schema+"."), args...)
}
func (p cabinetTestPool) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	return p.tx.QueryRow(ctx, strings.ReplaceAll(query, "wb.", p.schema+"."), args...)
}

// All fixture objects live in a unique schema inside a rolled-back transaction.
// No service tables or real transfer state are touched.
func TestCabinetMediaStatusIsPerItem(t *testing.T) {
	dsn := os.Getenv("WB_STATISTICS_TEST_DSN")
	if dsn == "" {
		t.Skip("set WB_STATISTICS_TEST_DSN to run PostgreSQL integration checks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	schema := fmt.Sprintf("cabinet_stats_test_%d", time.Now().UnixNano())
	if _, err := tx.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	const fixture = `
	CREATE TABLE wb.transfer_targets (id bigint, transfer_id bigint, position int, cabinet_id text);
	CREATE TABLE wb.transfer_groups (id bigint, transfer_id bigint, source_group_name text);
	CREATE TABLE wb.transfer_group_targets (
		id bigint, transfer_id bigint, source_group_id bigint, preparation_status text,
		publication_status text, media_status text, overall_outcome text, attention_code text
	);
	CREATE TABLE wb.statistics_item_facts (
		transfer_id bigint, item_target_id bigint, target_id bigint, cabinet_id text,
		group_target_id bigint, item_position int, vendor_code text, state text,
		outcome_class text, outcome_code text, nm_id bigint, attention_closed_at timestamptz,
		created_at timestamptz DEFAULT now(), started_at timestamptz, finished_at timestamptz
	);
	CREATE TABLE wb.publication_actions (id bigint, transfer_id bigint, kind text, state text, outcome_class text, outcome_code text);
	CREATE TABLE wb.publication_action_members (transfer_id bigint, transfer_item_target_id bigint, action_id bigint);
	INSERT INTO wb.transfer_targets VALUES (1, 1, 1, 'shop');
	INSERT INTO wb.transfer_groups VALUES (1, 1, 'keyrings'), (2, 1, 'other'), (3, 1, 'invalid');
	INSERT INTO wb.transfer_group_targets VALUES
		(1, 1, 1, 'succeeded', 'succeeded', 'unresolved', 'unresolved', 'MEDIA_REQUIRES_ATTENTION'),
		(2, 1, 2, 'succeeded', 'succeeded', 'running', 'running', NULL),
		(3, 1, 3, 'rejected', 'not_started', 'not_started', 'rejected', NULL);
	INSERT INTO wb.statistics_item_facts
		(transfer_id, item_target_id, target_id, cabinet_id, group_target_id, item_position, vendor_code, state, outcome_class, nm_id)
	VALUES
		(1, 101, 1, 'shop', 1, 1, 'A', 'terminal', 'success', 1001),
		(1, 102, 1, 'shop', 1, 2, 'B', 'terminal', 'success', 1002),
		(1, 103, 1, 'shop', 1, 3, 'C', 'terminal', 'skipped', 1003),
		(1, 104, 1, 'shop', 2, 4, 'D', 'terminal', 'success', 1004),
		(1, 105, 1, 'shop', 3, 5, 'E', 'terminal', 'rejected', NULL);
	INSERT INTO wb.publication_actions VALUES
		(1, 1, 'upload_media', 'terminal', 'unresolved', 'WB_MEDIA_TRANSPORT_RATE_LIMITED'),
		(2, 1, 'upload_media', 'terminal', 'success', 'WB_MEDIA_VISIBLE'),
		(3, 1, 'upload_media', 'reconciling', NULL, NULL),
		(4, 1, 'upload_media', 'superseded', NULL, NULL);
	INSERT INTO wb.publication_action_members VALUES (1, 101, 1), (1, 102, 2), (1, 104, 3), (1, 102, 4);
	`
	if _, err := tx.Exec(ctx, strings.ReplaceAll(fixture, "wb.", schema+".")); err != nil {
		t.Fatal(err)
	}
	repository := New(cabinetTestPool{tx: tx, schema: schema})
	progress, err := repository.ListCabinetProgress(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(progress) != 1 || progress[0].Total != 5 || progress[0].Pending != 0 || progress[0].Running != 1 ||
		progress[0].Terminal != 4 || progress[0].Ready != 2 || progress[0].Errors != 2 || progress[0].Attention != 1 {
		t.Fatalf("group failure leaked into sibling counters: %+v", progress)
	}
	page, err := repository.ListCabinetTasks(ctx, service.CabinetTaskFilter{TransferID: 1, TargetID: 1, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 5 || len(page.Tasks) != 5 {
		t.Fatalf("page=%+v", page)
	}
	for i, want := range []string{"unresolved", "success", "skipped", "running", "rejected"} {
		if page.Tasks[i].OverallOutcome != want {
			t.Fatalf("item %d: %+v, want %s", i, page.Tasks[i], want)
		}
	}
	if page.Tasks[0].MediaOutcomeCode != "WB_MEDIA_TRANSPORT_RATE_LIMITED" || page.Tasks[3].MediaActionState != "reconciling" {
		t.Fatalf("media details missing: %+v", page.Tasks)
	}
	page, err = repository.ListCabinetTasks(ctx, service.CabinetTaskFilter{TransferID: 1, TargetID: 1, Limit: 2, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 5 || len(page.Tasks) != 2 || page.Tasks[0].VendorCode != "B" || page.Tasks[1].VendorCode != "C" {
		t.Fatalf("media joins changed pagination: %+v", page)
	}
}
