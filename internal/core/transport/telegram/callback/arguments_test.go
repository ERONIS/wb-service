package core_tg_callback_test

import (
	"testing"

	core_tg_callback "github.com/ERONIS/wb-service/internal/core/transport/telegram/callback"
)

func TestBase36Helpers(t *testing.T) {
	id := int64(123456789)
	encoded := core_tg_callback.EncodeID(id)
	decoded, err := core_tg_callback.ParseID(encoded)
	if err != nil {
		t.Fatalf("ParseID failed: %v", err)
	}
	if decoded != id {
		t.Errorf("expected %d, got %d", id, decoded)
	}

	val, err := core_tg_callback.ParseBase36Argument([]string{"prefix", encoded}, 1, 2, 1)
	if err != nil {
		t.Fatalf("ParseBase36Argument failed: %v", err)
	}
	if val != id {
		t.Errorf("expected %d, got %d", id, val)
	}
}
