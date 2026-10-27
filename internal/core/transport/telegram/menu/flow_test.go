package core_tg_menu

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_tg_middleware "github.com/ERONIS/wb-service/internal/core/transport/telegram/middleware"
	tele "gopkg.in/telebot.v3"
)

type flowTestRoleProvider struct{}

func (flowTestRoleProvider) GetRole(context.Context, int64) (domain.UserRole, error) {
	return domain.RoleUser, nil
}

func TestDocumentFallbackWaitsForTextFlowToEnd(t *testing.T) {
	bot, err := tele.NewBot(tele.Settings{Token: "test", Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(bot, core_tg_middleware.NewRoleAccess(context.Background(), flowTestRoleProvider{}))
	handler.RegisterTextFlow("input", domain.RoleUser, func(tele.Context) (bool, error) {
		return true, nil
	})
	var documentCalls int
	handler.RegisterDocumentFallback(domain.RoleUser, func(tele.Context) error {
		documentCalls++
		return nil
	})
	ctx := bot.NewContext(tele.Update{Message: &tele.Message{
		Sender: &tele.User{ID: 42},
		Chat:   &tele.Chat{ID: 42},
	}})

	handler.BeginTextFlow(42, "input")
	if err := handler.handleDocumentFlow(ctx); err != nil {
		t.Fatal(err)
	}
	if documentCalls != 0 {
		t.Fatalf("document fallback ran during active text flow: %d calls", documentCalls)
	}

	handler.EndTextFlow(42, "input")
	if err := handler.handleDocumentFlow(ctx); err != nil {
		t.Fatal(err)
	}
	if documentCalls != 1 {
		t.Fatalf("document fallback after text flow = %d calls, want 1", documentCalls)
	}
}

func TestMainMenuReplyTakesPriorityOverActiveTextFlow(t *testing.T) {
	client := &http.Client{Transport: renderTestRoundTripper(func(r *http.Request) (*http.Response, error) {
		body := `{"ok":true,"result":true}`
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			body = `{"ok":true,"result":{"message_id":101,"chat":{"id":42,"type":"private"}}}`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}
	var handlerErr error
	bot, err := tele.NewBot(tele.Settings{
		Token:       "test",
		URL:         "https://telegram.test",
		Client:      client,
		Offline:     true,
		Synchronous: true,
		OnError: func(err error, _ tele.Context) {
			handlerErr = err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(bot, core_tg_middleware.NewRoleAccess(context.Background(), flowTestRoleProvider{}))
	handler.RegisterMainMenu()
	var textCalls int
	handler.RegisterTextFlow("input", domain.RoleUser, func(tele.Context) (bool, error) {
		textCalls++
		return true, nil
	})
	handler.BeginTextFlow(42, "input")
	bot.ProcessUpdate(tele.Update{Message: &tele.Message{
		ID:     100,
		Sender: &tele.User{ID: 42},
		Chat:   &tele.Chat{ID: 42},
		Text:   mainMenuReplyText,
	}})
	if handlerErr != nil {
		t.Fatal(handlerErr)
	}
	if textCalls != 0 {
		t.Fatalf("main menu reply reached active text flow: %d calls", textCalls)
	}
	bot.ProcessUpdate(tele.Update{Message: &tele.Message{
		ID:     101,
		Sender: &tele.User{ID: 42},
		Chat:   &tele.Chat{ID: 42},
		Text:   "next input",
	}})
	if handlerErr != nil {
		t.Fatal(handlerErr)
	}
	if textCalls != 0 {
		t.Fatalf("main menu reply left text flow active: %d calls", textCalls)
	}
}

func TestStaleTextFlowCannotEndSameKeyReplacement(t *testing.T) {
	bot, err := tele.NewBot(tele.Settings{Token: "test", Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(bot, core_tg_middleware.NewRoleAccess(context.Background(), flowTestRoleProvider{}))
	var cleanupCalls int
	handler.RegisterFlowCleanup("input", func(int64) {
		cleanupCalls++
	})
	var handlerCalls int
	handler.RegisterTextFlow("input", domain.RoleUser, func(tele.Context) (bool, error) {
		handlerCalls++
		if handlerCalls == 1 {
			handler.BeginTextFlow(42, "input")
			return false, nil
		}
		return true, nil
	})
	handler.BeginTextFlow(42, "input")
	ctx := bot.NewContext(tele.Update{Message: &tele.Message{
		Sender: &tele.User{ID: 42},
		Chat:   &tele.Chat{ID: 42},
		Text:   "value",
	}})
	if err := handler.handleTextFlow(ctx); err != nil {
		t.Fatal(err)
	}
	if err := handler.handleTextFlow(ctx); err != nil {
		t.Fatal(err)
	}
	if handlerCalls != 2 || cleanupCalls != 0 {
		t.Fatalf("replacement flow = (handlers %d, cleanup %d), want (2, 0)", handlerCalls, cleanupCalls)
	}
}

func TestCallbackNavigationEndsActiveTextFlowBeforeHandler(t *testing.T) {
	client := &http.Client{Transport: renderTestRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":true}`)),
		}, nil
	})}
	bot, err := tele.NewBot(tele.Settings{
		Token:   "test",
		URL:     "https://telegram.test",
		Client:  client,
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(bot, core_tg_middleware.NewRoleAccess(context.Background(), flowTestRoleProvider{}))
	handler.RegisterTextFlow("input", domain.RoleUser, func(tele.Context) (bool, error) {
		return true, nil
	})
	var cleanupCalls int
	handler.RegisterFlowCleanup("input", func(int64) {
		cleanupCalls++
	})
	handler.BeginTextFlow(42, "input")
	deniedButton := tele.Btn{Text: "Admin", Unique: "admin"}
	var deniedCalls int
	handler.RegisterCallback(deniedButton, domain.RoleAdmin, func(tele.Context) error {
		deniedCalls++
		return nil
	})
	deniedCtx := bot.NewContext(tele.Update{Callback: &tele.Callback{
		ID:      "denied",
		Sender:  &tele.User{ID: 42},
		Message: &tele.Message{ID: 101, Chat: &tele.Chat{ID: 42}},
	}})
	if err := bot.Trigger(&deniedButton, deniedCtx); err != nil {
		t.Fatal(err)
	}
	if deniedCalls != 0 || cleanupCalls != 0 {
		t.Fatalf("denied callback = (handlers %d, cleanup %d), want (0, 0)", deniedCalls, cleanupCalls)
	}
	button := tele.Btn{Text: "Other", Unique: "other"}
	var callbackCalls int
	handler.RegisterCallback(button, domain.RoleUser, func(tele.Context) error {
		callbackCalls++
		if cleanupCalls != 1 {
			t.Errorf("flow cleanup before callback = %d, want 1", cleanupCalls)
		}
		return nil
	})
	ctx := bot.NewContext(tele.Update{Callback: &tele.Callback{
		ID:      "callback",
		Sender:  &tele.User{ID: 42},
		Message: &tele.Message{ID: 101, Chat: &tele.Chat{ID: 42}},
	}})
	if err := bot.Trigger(&button, ctx); err != nil {
		t.Fatal(err)
	}
	if callbackCalls != 1 || cleanupCalls != 1 {
		t.Fatalf("callback = (handlers %d, cleanup %d), want (1, 1)", callbackCalls, cleanupCalls)
	}
}
