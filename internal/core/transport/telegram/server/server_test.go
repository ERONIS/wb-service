package core_tg_server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	tele "gopkg.in/telebot.v3"
)

type menuTestRoundTripper func(*http.Request) (*http.Response, error)

type menuTestPoller func(*tele.Bot, chan tele.Update, chan struct{})

func (transport menuTestRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func (poller menuTestPoller) Poll(bot *tele.Bot, updates chan tele.Update, stop chan struct{}) {
	poller(bot, updates, stop)
}

func TestRunConfiguresNativeMenuBeforePolling(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	var commandPayload map[string]json.RawMessage
	var menuPayload map[string]json.RawMessage
	client := &http.Client{Transport: menuTestRoundTripper(func(request *http.Request) (*http.Response, error) {
		method := strings.TrimPrefix(request.URL.Path, "/bottest/")
		var payload map[string]json.RawMessage
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			return nil, err
		}
		mu.Lock()
		methods = append(methods, method)
		switch method {
		case "setMyCommands":
			commandPayload = payload
		case "setChatMenuButton":
			menuPayload = payload
		}
		mu.Unlock()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":true}`)),
		}, nil
	})}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	bot, err := tele.NewBot(tele.Settings{
		Token:   "test",
		URL:     "https://telegram.test",
		Client:  client,
		Offline: true,
		Poller: menuTestPoller(func(_ *tele.Bot, _ chan tele.Update, stop chan struct{}) {
			mu.Lock()
			methods = append(methods, "poll")
			mu.Unlock()
			cancel()
			<-stop
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := (&Server{bot: bot}).Run(ctx); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(methods) != 3 || methods[0] != "setMyCommands" || methods[1] != "setChatMenuButton" || methods[2] != "poll" {
		t.Fatalf("methods = %v, want command setup, menu setup, then polling", methods)
	}
	var commands []tele.Command
	if err := json.Unmarshal(commandPayload["commands"], &commands); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 1 || commands[0].Text != "start" {
		t.Fatalf("commands = %+v, want only /start", commands)
	}
	var scope tele.CommandScope
	if err := json.Unmarshal(commandPayload["scope"], &scope); err != nil {
		t.Fatal(err)
	}
	if scope.Type != tele.CommandScopeAllPrivateChats {
		t.Fatalf("command scope = %q, want private chats", scope.Type)
	}
	var menuButton tele.MenuButton
	if err := json.Unmarshal(menuPayload["menu_button"], &menuButton); err != nil {
		t.Fatal(err)
	}
	if menuButton.Type != tele.MenuButtonCommands {
		t.Fatalf("menu button = %q, want commands", menuButton.Type)
	}
	if _, ok := menuPayload["chat_id"]; ok {
		t.Fatal("default menu button setup unexpectedly targets a chat")
	}
}

func TestRunMenuSetupErrorsStopPollingAndRedactToken(t *testing.T) {
	const token = "123456:ABC-Secret"
	for _, failedMethod := range []string{"setMyCommands", "setChatMenuButton"} {
		t.Run(failedMethod, func(t *testing.T) {
			var methods []string
			client := &http.Client{Transport: menuTestRoundTripper(func(request *http.Request) (*http.Response, error) {
				method := strings.TrimPrefix(request.URL.Path, "/bot"+token+"/")
				methods = append(methods, method)
				if method == failedMethod {
					return nil, errors.New("token=" + token)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":true}`)),
				}, nil
			})}
			polled := make(chan struct{}, 1)
			bot, err := tele.NewBot(tele.Settings{
				Token:   token,
				URL:     "https://telegram.test",
				Client:  client,
				Offline: true,
				Poller: menuTestPoller(func(_ *tele.Bot, _ chan tele.Update, stop chan struct{}) {
					polled <- struct{}{}
					<-stop
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			err = (&Server{bot: bot}).Run(context.Background())
			if err == nil || !strings.Contains(err.Error(), failedMethod) {
				t.Fatalf("setup error = %v, want %s failure", err, failedMethod)
			}
			if strings.Contains(err.Error(), token) || !strings.Contains(err.Error(), "<redacted>") {
				t.Fatalf("setup error exposed bot token: %v", err)
			}
			select {
			case <-polled:
				t.Fatal("polling started after menu setup failed")
			default:
			}
			if len(methods) == 0 || len(methods) > 2 || methods[len(methods)-1] != failedMethod {
				t.Fatalf("methods before failure = %v", methods)
			}
		})
	}
}
