package statistics_postgres_repository

import (
	"strings"
	"testing"
)

func TestOperationColumnsUsesEffectiveSourceSessionID(t *testing.T) {
	t.Parallel()

	const effectiveSource = "COALESCE(batch.source_session_id, batch.source_reference_id)"
	if !strings.Contains(operationColumns, effectiveSource) {
		t.Fatalf("operation columns do not select the effective source session: %q", operationColumns)
	}
}
