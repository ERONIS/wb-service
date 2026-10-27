package core_tg_menu

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	tele "gopkg.in/telebot.v3"
)

type accessTestRoleProvider struct {
	role  domain.UserRole
	err   error
	calls int
}

type accessTestStore struct {
	gets  int
	saves int
}

type accessTestAPI struct {
	mu      sync.Mutex
	methods []string
}

type accessTestFixture struct {
	bot        *tele.Bot
	provider   *accessTestRoleProvider
	store      *accessTestStore
	api        *accessTestAPI
	handlerErr error
	reached    int
}

func (provider *accessTestRoleProvider) GetRole(context.Context, int64) (domain.UserRole, error) {
	provider.calls++
	return provider.role, provider.err
}

func (store *accessTestStore) Get(context.Context, int64) (int, bool, error) {
	store.gets++
	return 0, false, nil
}

func (store *accessTestStore) Save(context.Context, int64, int) error {
	store.saves++
	return nil
}

func (api *accessTestAPI) RoundTrip(request *http.Request) (*http.Response, error) {
	method := strings.TrimPrefix(request.URL.Path, "/bottest/")
	api.mu.Lock()
	api.methods = append(api.methods, method)
	api.mu.Unlock()
	body := `{"ok":true,"result":true}`
	switch method {
	case "sendMessage", "editMessageText":
		body = `{"ok":true,"result":{"message_id":101,"chat":{"id":42,"type":"private"}}}`
	case "deleteMessage", "answerCallbackQuery":
	default:
		return nil, fmt.Errorf("unexpected Telegram method %q", method)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func (api *accessTestAPI) snapshotMethods() []string {
	api.mu.Lock()
	defer api.mu.Unlock()
	return append([]string(nil), api.methods...)
}

func newAccessTestFixture(t *testing.T, role domain.UserRole, roleErr error) *accessTestFixture {
	t.Helper()
	fixture := &accessTestFixture{
		provider: &accessTestRoleProvider{role: role, err: roleErr},
		store:    &accessTestStore{},
		api:      &accessTestAPI{},
	}
	bot, err := tele.NewBot(tele.Settings{
		Token:       "test",
		URL:         "https://telegram.test",
		Client:      &http.Client{Transport: fixture.api},
		Offline:     true,
		Synchronous: true,
		OnError: func(err error, _ tele.Context) {
			fixture.handlerErr = err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	fixture.bot = bot
	handler := Register(ctx, bot, fixture.provider, fixture.store)
	handler.RegisterCallback(tele.Btn{Text: "Protected", Unique: "protected"}, domain.RoleUser, func(tele.Context) error {
		fixture.reached++
		return nil
	})
	return fixture
}

func (fixture *accessTestFixture) assertNoMenuEffects(t *testing.T, wantRoleCalls int) {
	t.Helper()
	if fixture.provider.calls != wantRoleCalls || fixture.reached != 0 {
		t.Fatalf("role reads = %d, protected handler calls = %d", fixture.provider.calls, fixture.reached)
	}
	if fixture.store.gets != 0 || fixture.store.saves != 0 {
		t.Fatalf("menu store calls = (get %d, save %d), want none", fixture.store.gets, fixture.store.saves)
	}
	if methods := fixture.api.snapshotMethods(); len(methods) != 0 {
		t.Fatalf("Telegram API calls = %v, want none", methods)
	}
}

func TestUnregisteredUsersAreIgnoredBeforeMenuEffects(t *testing.T) {
	cases := []struct {
		name          string
		update        tele.Update
		wantRoleCalls int
	}{
		{
			name: "start command",
			update: tele.Update{Message: &tele.Message{
				ID: 100, Sender: &tele.User{ID: 42}, Chat: &tele.Chat{ID: 42}, Text: "/start",
			}},
			wantRoleCalls: 1,
		},
		{
			name: "ordinary text",
			update: tele.Update{Message: &tele.Message{
				ID: 100, Sender: &tele.User{ID: 42}, Chat: &tele.Chat{ID: 42}, Text: "hello",
			}},
			wantRoleCalls: 1,
		},
		{
			name: "document",
			update: tele.Update{Message: &tele.Message{
				ID: 100, Sender: &tele.User{ID: 42}, Chat: &tele.Chat{ID: 42}, Document: &tele.Document{},
			}},
			wantRoleCalls: 1,
		},
		{
			name: "registered callback",
			update: tele.Update{Callback: &tele.Callback{
				ID: "callback-1", Sender: &tele.User{ID: 42}, Message: &tele.Message{ID: 101, Chat: &tele.Chat{ID: 42}}, Data: "\fprotected",
			}},
			wantRoleCalls: 1,
		},
		{
			name: "unknown callback",
			update: tele.Update{Callback: &tele.Callback{
				ID: "callback-1", Sender: &tele.User{ID: 42}, Message: &tele.Message{ID: 101, Chat: &tele.Chat{ID: 42}}, Data: "\fmissing",
			}},
			wantRoleCalls: 1,
		},
		{
			name: "missing sender",
			update: tele.Update{Message: &tele.Message{
				ID: 100, Chat: &tele.Chat{ID: 42}, Text: "/start",
			}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newAccessTestFixture(t, "", core_errors.NotFound("user missing"))
			fixture.bot.ProcessUpdate(tc.update)
			if fixture.handlerErr != nil {
				t.Fatal(fixture.handlerErr)
			}
			fixture.assertNoMenuEffects(t, tc.wantRoleCalls)
		})
	}
}

func TestEmptyRoleIsIgnoredBeforeMenuEffects(t *testing.T) {
	fixture := newAccessTestFixture(t, "", nil)
	fixture.bot.ProcessUpdate(tele.Update{Message: &tele.Message{
		ID: 100, Sender: &tele.User{ID: 42}, Chat: &tele.Chat{ID: 42}, Text: "/start",
	}})
	if fixture.handlerErr != nil {
		t.Fatal(fixture.handlerErr)
	}
	fixture.assertNoMenuEffects(t, 1)
}

func TestRoleStorageFailureReachesOnErrorBeforeMenuEffects(t *testing.T) {
	storageErr := errors.New("role storage unavailable")
	fixture := newAccessTestFixture(t, "", storageErr)
	fixture.bot.ProcessUpdate(tele.Update{Callback: &tele.Callback{
		ID: "callback-1", Sender: &tele.User{ID: 42}, Message: &tele.Message{ID: 101, Chat: &tele.Chat{ID: 42}}, Data: "\fprotected",
	}})
	if !errors.Is(fixture.handlerErr, storageErr) {
		t.Fatalf("handler error = %v, want role storage error", fixture.handlerErr)
	}
	fixture.assertNoMenuEffects(t, 1)
}

func TestAuthorizedRoleIsReadOncePerUpdate(t *testing.T) {
	cases := []struct {
		name        string
		update      tele.Update
		wantReached int
	}{
		{
			name: "start command",
			update: tele.Update{Message: &tele.Message{
				ID: 100, Sender: &tele.User{ID: 42}, Chat: &tele.Chat{ID: 42}, Text: "/start",
			}},
		},
		{
			name: "callback",
			update: tele.Update{Callback: &tele.Callback{
				ID: "callback-1", Sender: &tele.User{ID: 42}, Message: &tele.Message{ID: 101, Chat: &tele.Chat{ID: 42}}, Data: "\fprotected",
			}},
			wantReached: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newAccessTestFixture(t, domain.RoleUser, nil)
			fixture.bot.ProcessUpdate(tc.update)
			if fixture.handlerErr != nil {
				t.Fatal(fixture.handlerErr)
			}
			if fixture.provider.calls != 1 || fixture.reached != tc.wantReached {
				t.Fatalf("role reads = %d, protected handler calls = %d, want 1 and %d", fixture.provider.calls, fixture.reached, tc.wantReached)
			}
			if methods := fixture.api.snapshotMethods(); len(methods) == 0 {
				t.Fatal("authorized update produced no Telegram response")
			}
		})
	}
}
