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

type callbackTestAnswer struct {
	CallbackID string `json:"callback_query_id"`
	Text       string `json:"text"`
	ShowAlert  bool   `json:"show_alert"`
}

type callbackTestAPI struct {
	mu      sync.Mutex
	answers []callbackTestAnswer
}

type callbackTestFixture struct {
	bot        *tele.Bot
	handler    *Handler
	api        *callbackTestAPI
	handlerErr error
}

func (api *callbackTestAPI) RoundTrip(request *http.Request) (*http.Response, error) {
	method := strings.TrimPrefix(request.URL.Path, "/bottest/")
	body := `{"ok":true,"result":true}`
	switch method {
	case "answerCallbackQuery":
		var answer callbackTestAnswer
		if err := json.NewDecoder(request.Body).Decode(&answer); err != nil {
			return nil, err
		}
		api.mu.Lock()
		api.answers = append(api.answers, answer)
		api.mu.Unlock()
	case "sendMessage":
		body = `{"ok":true,"result":{"message_id":201,"chat":{"id":42,"type":"private"}}}`
	case "deleteMessage":
	default:
		return nil, fmt.Errorf("unexpected Telegram method %q", method)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func (api *callbackTestAPI) snapshotAnswers() []callbackTestAnswer {
	api.mu.Lock()
	defer api.mu.Unlock()
	return append([]callbackTestAnswer(nil), api.answers...)
}

func newCallbackTestFixture(t *testing.T) *callbackTestFixture {
	t.Helper()
	fixture := &callbackTestFixture{api: &callbackTestAPI{}}
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
	fixture.handler = Register(ctx, bot, flowTestRoleProvider{}, &renderTestStore{})
	return fixture
}

func (fixture *callbackTestFixture) processCallback(data string) {
	fixture.bot.ProcessUpdate(tele.Update{Callback: &tele.Callback{
		ID:      "callback-1",
		Sender:  &tele.User{ID: 42},
		Message: &tele.Message{ID: 101, Chat: &tele.Chat{ID: 42}},
		Data:    data,
	}})
}

func TestExplicitCallbackAlertIsAnsweredOnce(t *testing.T) {
	cases := []struct {
		name    string
		handler tele.HandlerFunc
	}{
		{
			name: "direct",
			handler: func(ctx tele.Context) error {
				return ctx.RespondAlert("Нужен другой экран")
			},
		},
		{
			name: "notify",
			handler: func(ctx tele.Context) error {
				return Notify(ctx, "callback.alert", "Нужен другой экран")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newCallbackTestFixture(t)
			button := tele.Btn{Text: "Alert", Unique: "alert"}
			fixture.handler.RegisterCallback(button, domain.RoleUser, tc.handler)
			fixture.processCallback("\falert")
			if fixture.handlerErr != nil {
				t.Fatal(fixture.handlerErr)
			}
			answers := fixture.api.snapshotAnswers()
			if len(answers) != 1 || answers[0].CallbackID != "callback-1" ||
				answers[0].Text != "Нужен другой экран" || !answers[0].ShowAlert {
				t.Fatalf("callback answers = %+v, want one explicit alert", answers)
			}
		})
	}
}

func TestUnknownCallbackGetsAlert(t *testing.T) {
	fixture := newCallbackTestFixture(t)
	fixture.processCallback("\fno_such_button")
	if fixture.handlerErr != nil {
		t.Fatal(fixture.handlerErr)
	}
	answers := fixture.api.snapshotAnswers()
	if len(answers) != 1 || answers[0].CallbackID != "callback-1" ||
		!answers[0].ShowAlert || answers[0].Text == "" {
		t.Fatalf("unknown callback answers = %+v, want one alert", answers)
	}
}
