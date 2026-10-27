package core_tg_middleware_test

import (
	"errors"
	"strings"
	"testing"

	core_tg_middleware "github.com/ERONIS/wb-service/internal/core/transport/telegram/middleware"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	tele "gopkg.in/telebot.v3"
)

func TestErrorHandlerLogsHandlerErrorOnce(t *testing.T) {
	const token = "123456:ABC-Secret"
	core, logs := observer.New(zap.DebugLevel)
	logger := zap.New(core)
	bot, err := tele.NewBot(tele.Settings{
		Token:       token,
		Offline:     true,
		Synchronous: true,
		OnError:     core_tg_middleware.ErrorHandler(logger, token),
	})
	if err != nil {
		t.Fatalf("create offline bot: %v", err)
	}
	bot.Use(core_tg_middleware.Logger(logger))
	bot.Handle(tele.OnText, func(tele.Context) error {
		return errors.New("POST https://api.telegram.org/bot" + token + "/sendMessage: connection refused")
	})

	bot.ProcessUpdate(tele.Update{
		ID: 42,
		Message: &tele.Message{
			ID:     7,
			Text:   "hello",
			Sender: &tele.User{ID: 11},
			Chat:   &tele.Chat{ID: 11},
		},
	})

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("expected one error log from OnError, got %d: %+v", len(entries), entries)
	}
	entry := entries[0]
	if entry.Level != zapcore.ErrorLevel || entry.Message != "telegram update processing failed" {
		t.Fatalf("unexpected log entry: %+v", entry)
	}
	fields := entry.ContextMap()
	message, ok := fields["error"].(string)
	if !ok {
		t.Fatalf("missing error field: %+v", fields)
	}
	if strings.Contains(message, token) || !strings.Contains(message, "/bot<redacted>/sendMessage") {
		t.Fatalf("bot token was not redacted from URL: %q", message)
	}
	if fields["update_id"] != int64(42) || fields["sender_id"] != int64(11) || fields["message_id"] != int64(7) {
		t.Fatalf("missing update context: %+v", fields)
	}
}

func TestErrorHandlerRedactsStandaloneToken(t *testing.T) {
	const token = "123456:ABC-Secret"
	core, logs := observer.New(zap.DebugLevel)
	core_tg_middleware.ErrorHandler(zap.New(core), token)(errors.New("telegram token="+token), nil)

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("expected one error log, got %d", len(entries))
	}
	message, ok := entries[0].ContextMap()["error"].(string)
	if !ok || strings.Contains(message, token) || !strings.Contains(message, "token=<redacted>") {
		t.Fatalf("standalone token was not redacted: %q", message)
	}
}

func TestErrorHandlerAcceptsNilContext(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	core_tg_middleware.ErrorHandler(zap.New(core), "")(errors.New("poller stopped"), nil)

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("expected one error log, got %d", len(entries))
	}
	entry := entries[0]
	if entry.Level != zapcore.ErrorLevel || entry.Message != "telegram bot failed" {
		t.Fatalf("unexpected nil-context log entry: %+v", entry)
	}
	fields := entry.ContextMap()
	if fields["error"] != "poller stopped" {
		t.Fatalf("unexpected nil-context fields: %+v", fields)
	}
	if _, ok := fields["update_id"]; ok {
		t.Fatalf("nil context unexpectedly produced update fields: %+v", fields)
	}
}
