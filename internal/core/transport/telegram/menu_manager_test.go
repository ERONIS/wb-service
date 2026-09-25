package core_transport_telegram

import (
	"strings"
	"testing"
	"time"

	tele "gopkg.in/telebot.v3"
)

type alertContext struct {
	tele.Context
	callback *tele.Callback
	alert    string
}

func (ctx *alertContext) Callback() *tele.Callback {
	return ctx.callback
}

func (ctx *alertContext) RespondAlert(text string) error {
	ctx.alert = text
	return nil
}

func TestNotifyUsesTelegramAlertForCallback(t *testing.T) {
	t.Parallel()

	ctx := &alertContext{callback: &tele.Callback{ID: "callback-id"}}
	if err := Notify(ctx, "test.error", "Ошибка операции"); err != nil {
		t.Fatalf("notify callback: %v", err)
	}
	if ctx.alert != "Ошибка операции" {
		t.Fatalf("unexpected alert text: %q", ctx.alert)
	}
}

func TestDuplicateNotification(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	previous := notificationState{shownAt: map[string]time.Time{
		"invalid_xlsx": now,
	}}
	if !duplicateNotification(previous, "invalid_xlsx", now.Add(time.Second)) {
		t.Fatal("same notification inside dedupe window was not suppressed")
	}
	if duplicateNotification(previous, "session_conflict", now.Add(time.Second)) {
		t.Fatal("different notification was suppressed")
	}
	if duplicateNotification(previous, "invalid_xlsx", now.Add(notificationDedupeFor)) {
		t.Fatal("notification after dedupe window was suppressed")
	}
}

func TestDuplicateNotificationTracksEveryErrorKey(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	previous := notificationState{shownAt: map[string]time.Time{
		"invalid_xlsx":     now,
		"session_conflict": now.Add(time.Second),
	}}
	if !duplicateNotification(previous, "invalid_xlsx", now.Add(2*time.Second)) {
		t.Fatal("first error key was forgotten after another error")
	}
	if !duplicateNotification(previous, "session_conflict", now.Add(2*time.Second)) {
		t.Fatal("second error key was not suppressed")
	}
}

func TestNotificationTextEscapesNotice(t *testing.T) {
	t.Parallel()

	result := notificationText("Ошибка <файла>")
	if !strings.Contains(result, "Ошибка &lt;файла&gt;") {
		t.Fatalf("notification was not escaped: %q", result)
	}
}

func TestDeletedMessageMissing(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: true,
		},
		{
			name:     "tele.ErrNotFoundToDelete",
			err:      tele.ErrNotFoundToDelete,
			expected: true,
		},
		{
			name:     "tele.ErrNoRightsToDelete",
			err:      tele.ErrNoRightsToDelete,
			expected: true,
		},
		{
			name:     "string contains message to delete not found",
			err:      tele.NewError(400, "Bad Request: message to delete not found"),
			expected: true,
		},
		{
			name:     "string contains message can't be deleted",
			err:      tele.NewError(400, "Bad Request: message can't be deleted"),
			expected: true,
		},
		{
			name:     "string contains message_id_invalid",
			err:      tele.NewError(400, "Bad Request: message_id_invalid"),
			expected: true,
		},
		{
			name:     "unrelated error",
			err:      tele.NewError(400, "Bad Request: chat not found"),
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if actual := deletedMessageMissing(tc.err); actual != tc.expected {
				t.Fatalf("deletedMessageMissing(%v) = %v, want %v", tc.err, actual, tc.expected)
			}
		})
	}
}

func TestMissingMessageError(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "tele.ErrCantEditMessage",
			err:      tele.ErrCantEditMessage,
			expected: true,
		},
		{
			name:     "string contains message to edit not found",
			err:      tele.NewError(400, "Bad Request: message to edit not found"),
			expected: true,
		},
		{
			name:     "unrelated error",
			err:      tele.NewError(400, "Bad Request: text is empty"),
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if actual := missingMessageError(tc.err); actual != tc.expected {
				t.Fatalf("missingMessageError(%v) = %v, want %v", tc.err, actual, tc.expected)
			}
		})
	}
}
