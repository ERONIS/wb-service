package users_transport_tg

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_tg_menu "github.com/ERONIS/wb-service/internal/core/transport/telegram/menu"
	users_service "github.com/ERONIS/wb-service/internal/feature/users/service"
	tele "gopkg.in/telebot.v3"
)

type usersMenuTestRoleProvider struct{}

type usersMenuTestStore struct {
	messageID int
}

type usersMenuTestService struct {
	UsersService
	listPages []int
}

type usersMenuTestRequest struct {
	method string
	fields map[string]json.RawMessage
}

type usersMenuTestAPI struct {
	requests []usersMenuTestRequest
}

type usersMenuTestButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

type usersMenuTestMarkup struct {
	InlineKeyboard [][]usersMenuTestButton `json:"inline_keyboard"`
}

func (usersMenuTestRoleProvider) GetRole(context.Context, int64) (domain.UserRole, error) {
	return domain.RoleAdmin, nil
}

func (store *usersMenuTestStore) Get(context.Context, int64) (int, bool, error) {
	return store.messageID, store.messageID != 0, nil
}

func (store *usersMenuTestStore) Save(_ context.Context, _ int64, messageID int) error {
	store.messageID = messageID
	return nil
}

func (service *usersMenuTestService) GetUsers(_ context.Context, adminTelegramID int64, page int) (users_service.UsersPage, error) {
	if adminTelegramID != 42 {
		return users_service.UsersPage{}, fmt.Errorf("unexpected admin Telegram ID: %d", adminTelegramID)
	}
	service.listPages = append(service.listPages, page)
	return users_service.UsersPage{
		Users:   []domain.User{{TelegramID: 777, FullName: "Тестовый пользователь", Role: domain.RoleUser}},
		Page:    page,
		HasNext: page == 1,
	}, nil
}

func (api *usersMenuTestAPI) RoundTrip(request *http.Request) (*http.Response, error) {
	method := strings.TrimPrefix(request.URL.Path, "/bottest/")
	fields := make(map[string]json.RawMessage)
	if err := json.NewDecoder(request.Body).Decode(&fields); err != nil {
		return nil, err
	}
	api.requests = append(api.requests, usersMenuTestRequest{method: method, fields: fields})

	result := `{"ok":true,"result":true}`
	switch method {
	case "editMessageText":
		result = `{"ok":true,"result":{"message_id":101,"chat":{"id":42,"type":"private"}}}`
	case "answerCallbackQuery":
	default:
		return nil, fmt.Errorf("unexpected Telegram method %q", method)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(result)),
	}, nil
}

func (request usersMenuTestRequest) text(t *testing.T) string {
	t.Helper()
	var value string
	if err := json.Unmarshal(request.fields["text"], &value); err != nil {
		t.Fatalf("decode Telegram message text: %v", err)
	}
	return value
}

func (request usersMenuTestRequest) markup(t *testing.T) usersMenuTestMarkup {
	t.Helper()
	var encoded string
	if err := json.Unmarshal(request.fields["reply_markup"], &encoded); err != nil {
		t.Fatalf("decode Telegram reply markup field: %v", err)
	}
	var markup usersMenuTestMarkup
	if err := json.Unmarshal([]byte(encoded), &markup); err != nil {
		t.Fatalf("decode Telegram inline keyboard: %v", err)
	}
	return markup
}

func (markup usersMenuTestMarkup) hasButton(text, callbackData string) bool {
	for _, row := range markup.InlineKeyboard {
		for _, button := range row {
			if button.Text == text && button.CallbackData == callbackData {
				return true
			}
		}
	}
	return false
}

func TestUsersMenuOpensListOnlyAfterAllUsersButton(t *testing.T) {
	api := &usersMenuTestAPI{}
	var handlerErr error
	bot, err := tele.NewBot(tele.Settings{
		Token:       "test",
		URL:         "https://telegram.test",
		Client:      &http.Client{Transport: api},
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
	service := &usersMenuTestService{}
	store := &usersMenuTestStore{messageID: 101}
	menu := core_tg_menu.Register(ctx, bot, usersMenuTestRoleProvider{}, store)
	NewUsersTgHandler(ctx, service).Register(menu)

	press := func(data string) {
		t.Helper()
		bot.ProcessUpdate(tele.Update{Callback: &tele.Callback{
			ID:      "callback-1",
			Sender:  &tele.User{ID: 42},
			Message: &tele.Message{ID: 101, Chat: &tele.Chat{ID: 42}},
			Data:    "\f" + data,
		}})
		if handlerErr != nil {
			t.Fatal(handlerErr)
		}
	}
	lastEdit := func() usersMenuTestRequest {
		t.Helper()
		for index := len(api.requests) - 1; index >= 0; index-- {
			if api.requests[index].method == "editMessageText" {
				return api.requests[index]
			}
		}
		t.Fatal("Telegram menu was not edited")
		return usersMenuTestRequest{}
	}

	press("users_menu")
	if len(service.listPages) != 0 {
		t.Fatalf("list requests after opening Users = %v, want none", service.listPages)
	}
	root := lastEdit()
	if strings.Contains(root.text(t), "Тестовый пользователь") {
		t.Fatalf("Users submenu contains a user record: %q", root.text(t))
	}
	if !root.markup(t).hasButton("👥 Все пользователи", "\fusers_list") {
		t.Fatalf("Users submenu has no All users button: %+v", root.markup(t))
	}

	press("users_list")
	if len(service.listPages) != 1 || service.listPages[0] != 1 {
		t.Fatalf("list requests after All users = %v, want page 1", service.listPages)
	}
	list := lastEdit()
	if !strings.Contains(list.text(t), "Тестовый пользователь") || !strings.Contains(list.text(t), "Страница: 1") {
		t.Fatalf("All users screen = %q, want paginated user list", list.text(t))
	}
	if !list.markup(t).hasButton("Далее ➡️", "\fusers_list|2") {
		t.Fatalf("All users screen has no next-page button: %+v", list.markup(t))
	}
	if !list.markup(t).hasButton("⬅️ Пользователи", "\fusers_menu") {
		t.Fatalf("All users screen has no return-to-Users button: %+v", list.markup(t))
	}

	press("users_menu")
	if len(service.listPages) != 1 {
		t.Fatalf("list requests after returning to Users = %v, want only page 1", service.listPages)
	}
	returned := lastEdit()
	if !returned.markup(t).hasButton("👥 Все пользователи", "\fusers_list") || strings.Contains(returned.text(t), "Тестовый пользователь") {
		t.Fatalf("returned Users submenu = (%q, %+v), want submenu without records", returned.text(t), returned.markup(t))
	}
	for _, request := range api.requests {
		if request.method == "sendMessage" {
			t.Fatal("Users navigation sent a second chat message")
		}
	}
}
