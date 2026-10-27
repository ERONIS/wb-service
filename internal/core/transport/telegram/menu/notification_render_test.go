package core_tg_menu

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/ERONIS/wb-service/internal/core/domain"
	tele "gopkg.in/telebot.v3"
)

type notificationTestRequest struct {
	method    string
	text      string
	markup    string
	messageID string
}

type notificationTestTransport struct {
	mu            sync.Mutex
	requests      []notificationTestRequest
	nextMessageID int
}

func (transport *notificationTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	method := strings.TrimPrefix(request.URL.Path, "/bottest/")
	var payload map[string]string
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		return nil, err
	}

	transport.mu.Lock()
	transport.requests = append(transport.requests, notificationTestRequest{
		method:    method,
		text:      payload["text"],
		markup:    payload["reply_markup"],
		messageID: payload["message_id"],
	})
	messageID := transport.nextMessageID
	if method == "sendMessage" {
		transport.nextMessageID++
		messageID = transport.nextMessageID
	}
	if method == "editMessageText" {
		parsed, err := strconv.Atoi(payload["message_id"])
		if err != nil {
			transport.mu.Unlock()
			return nil, err
		}
		messageID = parsed
	}
	transport.mu.Unlock()

	body := `{"ok":true,"result":true}`
	if method == "sendMessage" || method == "editMessageText" {
		body = fmt.Sprintf(`{"ok":true,"result":{"message_id":%d,"chat":{"id":42,"type":"private"},"text":%q}}`, messageID, payload["text"])
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func (transport *notificationTestTransport) snapshot() []notificationTestRequest {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return append([]notificationTestRequest(nil), transport.requests...)
}

func newNotificationTestBot(t *testing.T, transport *notificationTestTransport) *tele.Bot {
	t.Helper()
	bot, err := tele.NewBot(tele.Settings{
		Token:       "test",
		URL:         "https://telegram.test",
		Client:      &http.Client{Transport: transport},
		Offline:     true,
		Synchronous: true,
		OnError: func(err error, _ tele.Context) {
			t.Errorf("Telegram handler: %v", err)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return bot
}

func notificationTestText(bot *tele.Bot, messageID int, text string) {
	bot.ProcessUpdate(tele.Update{Message: &tele.Message{
		ID:     messageID,
		Sender: &tele.User{ID: 42},
		Chat:   &tele.Chat{ID: 42},
		Text:   text,
	}})
}

func notificationTestInlineButton(t *testing.T, raw string, text string) bool {
	t.Helper()
	var markup tele.ReplyMarkup
	if err := json.Unmarshal([]byte(raw), &markup); err != nil {
		t.Fatalf("inline markup %q: %v", raw, err)
	}
	for _, row := range markup.InlineKeyboard {
		for _, button := range row {
			if button.Text == text {
				return true
			}
		}
	}
	return false
}

func TestTextNotificationStaysInActiveMenuAndNextInputRestoresScreen(t *testing.T) {
	transport := &notificationTestTransport{nextMessageID: 100}
	bot := newNotificationTestBot(t, transport)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &renderTestStore{}
	handler := Register(ctx, bot, flowTestRoleProvider{}, store)
	page := bot.NewMarkup()
	page.Inline(page.Row(tele.Btn{Text: "Остаться", Unique: "stay_test"}))

	notificationTestText(bot, 100, "/start")
	if err := handler.RenderChat(42, "Текущий экран", page); err != nil {
		t.Fatal(err)
	}
	callCount := 0
	handler.RegisterTextFlow("notification_test", domain.RoleUser, func(ctx tele.Context) (bool, error) {
		callCount++
		if callCount == 1 {
			return true, Notify(ctx, "notification.invalid", "⚠️ Неверный ввод")
		}
		return false, nil
	})
	handler.BeginTextFlow(42, "notification_test")
	notificationTestText(bot, 102, "неверное значение")

	requests := transport.snapshot()
	sends := 0
	var notice notificationTestRequest
	for _, request := range requests {
		if request.method == "sendMessage" {
			sends++
		}
		if request.method == "editMessageText" && strings.Contains(request.text, "Неверный ввод") {
			notice = request
		}
	}
	if sends != 1 {
		t.Fatalf("sendMessage count after notification = %d, want only initial menu; requests = %+v", sends, requests)
	}
	if notice.method == "" || notice.messageID != "101" || !strings.Contains(notice.text, "Текущий экран") {
		t.Fatalf("notification did not edit current menu 101: %+v", requests)
	}
	if !notificationTestInlineButton(t, notice.markup, "Остаться") {
		t.Fatalf("notification lost current screen button: %+v", notice)
	}

	notificationTestText(bot, 103, "следующее значение")
	requests = transport.snapshot()
	var restored notificationTestRequest
	for _, request := range requests {
		if request.method == "sendMessage" || request.method == "editMessageText" {
			restored = request
		}
	}
	if callCount != 2 || restored.text != "Текущий экран" {
		t.Fatalf("next input did not restore original screen: calls=%d, last=%+v", callCount, restored)
	}
	if !notificationTestInlineButton(t, restored.markup, "Остаться") {
		t.Fatalf("restored screen lost inline button: %+v", restored)
	}
}

func TestTextNotificationAfterRestartEditsPersistedMenuWithReturnButton(t *testing.T) {
	transport := &notificationTestTransport{nextMessageID: 101}
	bot := newNotificationTestBot(t, transport)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &renderTestStore{messageID: 101}
	handler := Register(ctx, bot, flowTestRoleProvider{}, store)
	handler.RegisterTextFlow("notification_test", domain.RoleUser, func(ctx tele.Context) (bool, error) {
		return true, Notify(ctx, "notification.after_restart", "⚠️ Проверка после перезапуска")
	})
	handler.BeginTextFlow(42, "notification_test")
	notificationTestText(bot, 102, "ошибочный ввод")

	requests := transport.snapshot()
	edits := 0
	for _, request := range requests {
		if request.method == "sendMessage" {
			t.Fatalf("notification after restart sent a new message: %+v", requests)
		}
		if request.method != "editMessageText" {
			continue
		}
		edits++
		if request.messageID != "101" || !strings.Contains(request.text, "Проверка после перезапуска") {
			t.Fatalf("notification edited wrong menu: %+v", request)
		}
		if !notificationTestInlineButton(t, request.markup, "🏠 Главное меню") {
			t.Fatalf("restart fallback has no return button: %+v", request)
		}
	}
	if edits != 1 {
		t.Fatalf("notification after restart edits = %d, want 1; requests = %+v", edits, requests)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.messageID != 101 || store.saves != 0 {
		t.Fatalf("persisted menu changed after notification: id=%d saves=%d", store.messageID, store.saves)
	}
}
