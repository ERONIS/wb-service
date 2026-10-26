package core_tg_middleware_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_tg_middleware "github.com/ERONIS/wb-service/internal/core/transport/telegram/middleware"
	tele "gopkg.in/telebot.v3"
)

type roleTestProvider struct {
	role  domain.UserRole
	err   error
	calls int
}

type roleTestRoundTripper func(*http.Request) (*http.Response, error)

func (provider *roleTestProvider) GetRole(context.Context, int64) (domain.UserRole, error) {
	provider.calls++
	return provider.role, provider.err
}

func (roundTrip roleTestRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestRequireHandlesMissingLowerAndFailedRoles(t *testing.T) {
	storageErr := errors.New("role storage unavailable")
	cases := []struct {
		name        string
		role        domain.UserRole
		providerErr error
		minimumRole domain.UserRole
		wantText    string
		wantErr     error
	}{
		{
			name:        "missing user",
			providerErr: core_errors.NotFound("user missing"),
			minimumRole: domain.RoleUser,
		},
		{
			name:        "lower role",
			role:        domain.RoleUser,
			minimumRole: domain.RoleAdmin,
			wantText:    "⛔ Недостаточно прав.",
		},
		{
			name:        "storage error",
			providerErr: storageErr,
			minimumRole: domain.RoleUser,
			wantText:    "❌ Не удалось проверить права. Попробуйте позже.",
			wantErr:     storageErr,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := &roleTestProvider{role: tc.role, err: tc.providerErr}
			var messages []string
			var handlerErr error
			handled := false
			client := &http.Client{Transport: roleTestRoundTripper(func(request *http.Request) (*http.Response, error) {
				method := strings.TrimPrefix(request.URL.Path, "/bottest/")
				if method != "sendMessage" {
					return nil, fmt.Errorf("unexpected Telegram method %q", method)
				}
				var payload struct {
					Text string `json:"text"`
				}
				if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
					return nil, err
				}
				messages = append(messages, payload.Text)
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_id":101,"chat":{"id":42,"type":"private"}}}`)),
				}, nil
			})}
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
			access := core_tg_middleware.NewRoleAccess(context.Background(), provider)
			bot.Handle("/private", func(tele.Context) error {
				handled = true
				return nil
			}, access.Require(tc.minimumRole))
			bot.ProcessUpdate(tele.Update{Message: &tele.Message{
				ID:     100,
				Sender: &tele.User{ID: 42},
				Chat:   &tele.Chat{ID: 42},
				Text:   "/private",
			}})

			if provider.calls != 1 || handled {
				t.Fatalf("provider calls = %d, handler called = %t", provider.calls, handled)
			}
			if tc.wantText == "" {
				if len(messages) != 0 {
					t.Fatalf("Telegram messages = %q, want none", messages)
				}
			} else if len(messages) != 1 || messages[0] != tc.wantText {
				t.Fatalf("Telegram messages = %q, want %q", messages, tc.wantText)
			}
			if !errors.Is(handlerErr, tc.wantErr) || (tc.wantErr == nil && handlerErr != nil) {
				t.Fatalf("handler error = %v, want %v", handlerErr, tc.wantErr)
			}
		})
	}
}
