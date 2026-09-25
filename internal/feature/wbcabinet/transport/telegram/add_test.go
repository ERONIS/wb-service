package wbcabinet_telegram_transport

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	wbcabinet_service "github.com/ERONIS/wb-service/internal/feature/wbcabinet/service"

	tele "gopkg.in/telebot.v3"
)

type addCabinetServiceStub struct {
	called  chan struct{}
	release chan struct{}
}

func (stub *addCabinetServiceStub) Add(
	context.Context,
	wbcabinet_service.AddCommand,
) (wbcabinet_service.Cabinet, error) {
	if stub.called != nil {
		close(stub.called)
	}
	if stub.release != nil {
		<-stub.release
	}
	return wbcabinet_service.Cabinet{}, errors.New("verification failed")
}

func (*addCabinetServiceStub) ListByOwner(
	context.Context,
	int64,
) ([]wbcabinet_service.Cabinet, error) {
	return nil, nil
}

func (*addCabinetServiceStub) Delete(
	context.Context,
	int64,
	wbcabinet_service.CabinetID,
) error {
	return nil
}

type addTextContext struct {
	tele.Context
	sender       *tele.User
	text         string
	deleted      bool
	notification string

	mu    sync.Mutex
	edits []string
}

func (ctx *addTextContext) Sender() *tele.User { return ctx.sender }
func (ctx *addTextContext) Text() string       { return ctx.text }

func (ctx *addTextContext) Delete() error {
	ctx.deleted = true
	return nil
}

func (ctx *addTextContext) NotifyMenu(_ string, text string) error {
	ctx.notification = text
	return nil
}

func (ctx *addTextContext) EditOrSend(what interface{}, _ ...interface{}) error {
	text, _ := what.(string)
	ctx.mu.Lock()
	ctx.edits = append(ctx.edits, text)
	ctx.mu.Unlock()
	return nil
}

func (ctx *addTextContext) edit(index int) string {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	if index < 0 || index >= len(ctx.edits) {
		return ""
	}
	return ctx.edits[index]
}

func TestBusyCabinetVerificationConsumesRepeatedToken(t *testing.T) {
	t.Parallel()

	handler := New(context.Background(), &addCabinetServiceStub{})
	handler.pending.Begin(42, pendingAdd{step: stepCabinetToken, name: "Основной"}, addCabinetTTL)
	lease, status := handler.pending.Claim(42)
	if status != core_transport_telegram.StateClaimed {
		t.Fatalf("initial state claim = %v", status)
	}
	ctx := &addTextContext{
		sender: &tele.User{ID: 42},
		text:   "second-sensitive-token",
	}

	handled, err := handler.handleText(ctx)
	if err != nil {
		t.Fatalf("handleText() error = %v", err)
	}
	if !handled || !ctx.deleted {
		t.Fatalf("repeated token was not consumed: handled=%v deleted=%v", handled, ctx.deleted)
	}
	if !strings.Contains(ctx.notification, "уже проверяется") {
		t.Fatalf("unexpected busy notification: %q", ctx.notification)
	}
	handler.pending.Finish(42, lease, false, 0)
}

func TestCabinetTokenShowsVerificationProgressBeforeRemoteCallCompletes(t *testing.T) {
	t.Parallel()

	service := &addCabinetServiceStub{
		called:  make(chan struct{}),
		release: make(chan struct{}),
	}
	handler := New(context.Background(), service)
	handler.pending.Begin(42, pendingAdd{step: stepCabinetToken, name: "Основной"}, addCabinetTTL)
	ctx := &addTextContext{
		sender: &tele.User{ID: 42},
		text:   "sensitive-token",
	}
	done := make(chan error, 1)
	go func() {
		_, err := handler.handleText(ctx)
		done <- err
	}()

	<-service.called
	if !ctx.deleted {
		t.Fatal("token message was not deleted before remote verification")
	}
	if progress := ctx.edit(0); !strings.Contains(progress, "Проверяю токен") {
		t.Fatalf("verification progress was not shown first: %q", progress)
	}
	close(service.release)
	if err := <-done; err != nil {
		t.Fatalf("handleText() error = %v", err)
	}
}
