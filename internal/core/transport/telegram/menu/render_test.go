package core_tg_menu

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/ERONIS/wb-service/internal/core/domain"
	tele "gopkg.in/telebot.v3"
)

type renderTestStore struct {
	mu        sync.Mutex
	messageID int
	saves     int
}

type renderTestRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip renderTestRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return roundTrip(r)
}

func (store *renderTestStore) Get(_ context.Context, _ int64) (int, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.messageID, store.messageID != 0, nil
}

func (store *renderTestStore) Save(_ context.Context, _ int64, messageID int) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.messageID = messageID
	store.saves++
	return nil
}

func TestRenderChatEditsPersistedScreenAfterHostRestart(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	client := &http.Client{Transport: renderTestRoundTripper(func(r *http.Request) (*http.Response, error) {
		method := strings.TrimPrefix(r.URL.Path, "/bottest/")
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			return nil, err
		}
		mu.Lock()
		methods = append(methods, method)
		mu.Unlock()
		if method == "editMessageText" && payload["message_id"] != "101" {
			return nil, fmt.Errorf("wrong persisted message ID: %q", payload["message_id"])
		}
		body := fmt.Sprintf(`{"ok":true,"result":{"message_id":101,"chat":{"id":42,"type":"private"},"text":%q}}`, payload["text"])
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
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
	store := &renderTestStore{}
	if err := newMenuHost(context.Background(), bot, store).renderChat(42, "first"); err != nil {
		t.Fatal(err)
	}
	if err := newMenuHost(context.Background(), bot, store).renderChat(42, "second"); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	gotMethods := append([]string(nil), methods...)
	mu.Unlock()
	if len(gotMethods) != 2 || gotMethods[0] != "sendMessage" || gotMethods[1] != "editMessageText" {
		t.Fatalf("Telegram methods = %v, want sendMessage then editMessageText", gotMethods)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.messageID != 101 || store.saves != 1 {
		t.Fatalf("persisted screen = (message %d, saves %d), want (101, 1)", store.messageID, store.saves)
	}
}

func TestStartSendsOnlyInlineMenu(t *testing.T) {
	var mu sync.Mutex
	var sentTexts []string
	var sentMarkups []string
	nextMessageID := 100
	client := &http.Client{Transport: renderTestRoundTripper(func(r *http.Request) (*http.Response, error) {
		method := strings.TrimPrefix(r.URL.Path, "/bottest/")
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			return nil, err
		}
		body := `{"ok":true,"result":true}`
		if method == "sendMessage" {
			mu.Lock()
			nextMessageID++
			messageID := nextMessageID
			sentTexts = append(sentTexts, payload["text"])
			sentMarkups = append(sentMarkups, payload["reply_markup"])
			mu.Unlock()
			body = fmt.Sprintf(`{"ok":true,"result":{"message_id":%d,"chat":{"id":42,"type":"private"}}}`, messageID)
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &renderTestStore{}
	handler := Register(ctx, bot, flowTestRoleProvider{}, store)
	handler.RegisterMenuItem(tele.Btn{Text: "Профиль", Unique: "profile_test"}, domain.RoleUser, func(tele.Context) error {
		return nil
	})
	bot.ProcessUpdate(tele.Update{Message: &tele.Message{
		ID:     100,
		Sender: &tele.User{ID: 42},
		Chat:   &tele.Chat{ID: 42},
		Text:   "/start",
	}})
	if handlerErr != nil {
		t.Fatal(handlerErr)
	}
	mu.Lock()
	gotTexts := append([]string(nil), sentTexts...)
	gotMarkups := append([]string(nil), sentMarkups...)
	mu.Unlock()
	if len(gotTexts) != 1 || gotTexts[0] != mainMenuText {
		t.Fatalf("/start sent messages %q, want only the main menu", gotTexts)
	}
	var markup tele.ReplyMarkup
	if err := json.Unmarshal([]byte(gotMarkups[0]), &markup); err != nil {
		t.Fatalf("main menu reply markup: %v", err)
	}
	if len(markup.InlineKeyboard) == 0 || len(markup.ReplyKeyboard) != 0 {
		t.Fatalf("main menu markup = %+v, want only inline buttons", markup)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.messageID != 101 || store.saves != 1 {
		t.Fatalf("persisted screen = (message %d, saves %d), want (101, 1)", store.messageID, store.saves)
	}
}

func TestRepeatedStartEditsExistingMenu(t *testing.T) {
	var methods []string
	var editedMessageID string
	var failedDeletes int
	client := &http.Client{Transport: renderTestRoundTripper(func(r *http.Request) (*http.Response, error) {
		method := strings.TrimPrefix(r.URL.Path, "/bottest/")
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			return nil, err
		}
		result := `{"ok":true,"result":true}`
		if method == "deleteMessage" {
			failedDeletes++
			result = `{"ok":false,"error_code":400,"description":"Bad Request: message can't be deleted"}`
		}
		if method == "sendMessage" || method == "editMessageText" {
			methods = append(methods, method)
			if method == "editMessageText" {
				editedMessageID = payload["message_id"]
			}
			result = fmt.Sprintf(`{"ok":true,"result":{"message_id":101,"chat":{"id":42,"type":"private"},"text":%q}}`, payload["text"])
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(result)),
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &renderTestStore{}
	Register(ctx, bot, flowTestRoleProvider{}, store)

	for _, messageID := range []int{100, 102} {
		bot.ProcessUpdate(tele.Update{Message: &tele.Message{
			ID:     messageID,
			Sender: &tele.User{ID: 42},
			Chat:   &tele.Chat{ID: 42},
			Text:   "/start",
		}})
	}
	if handlerErr != nil {
		t.Fatal(handlerErr)
	}
	if failedDeletes != 2 {
		t.Fatalf("failed delete requests = %d, want 2", failedDeletes)
	}
	if len(methods) != 2 || methods[0] != "sendMessage" || methods[1] != "editMessageText" {
		t.Fatalf("Telegram menu methods = %v, want one send then one edit", methods)
	}
	if editedMessageID != "101" {
		t.Fatalf("edited message ID = %q, want 101", editedMessageID)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.messageID != 101 || store.saves != 1 {
		t.Fatalf("persisted screen = (message %d, saves %d), want (101, 1)", store.messageID, store.saves)
	}
}
