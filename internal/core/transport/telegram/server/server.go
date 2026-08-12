package core_tg_server

import (
	"context"
	"fmt"
	"net/http"

	core_tg_middleware "github.com/ERONIS/wb-service/internal/core/transport/telegram/middleware"

	"go.uber.org/zap"
	tele "gopkg.in/telebot.v3"
)

type Server struct {
	bot *tele.Bot
}

func New(
	cfg Config,
	logger *zap.Logger,
) (*Server, error) {
	if logger == nil {
		return nil, fmt.Errorf("telegram server logger is nil")
	}

	bot, err := tele.NewBot(tele.Settings{
		Token: cfg.Token,
		Poller: &tele.LongPoller{
			Timeout: cfg.PollTimeout},
		Client: &http.Client{
			Timeout: cfg.HTTPTimeout,
		},
		Updates:     cfg.UpdatesBuffer,
		Synchronous: cfg.Synchronous,
		Verbose:     cfg.Verbose,
		Offline:     cfg.Offline,
		ParseMode:   tele.ModeHTML,
	})
	if err != nil {
		return nil, fmt.Errorf("create telegram bot: %w", err)
	}
	bot.Use(core_tg_middleware.Logger(logger))

	return &Server{
		bot: bot,
	}, nil
}

func (s *Server) Bot() *tele.Bot {
	return s.bot
}
func (s *Server) Run(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("telegram server context is nil")
	}
	if ctx.Err() != nil {
		return fmt.Errorf("telegram server context error: %w", ctx.Err())
	}

	done := make(chan struct{})

	go func() {
		defer close(done)
		s.bot.Start()
	}()

	select {
	case <-ctx.Done():
		s.bot.Stop()
		<-done
		return nil
	case <-done:
		return nil
	}

}
