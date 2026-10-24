package cardimport_telegram_transport

import (
	"context"
	cardimport_contract "github.com/ERONIS/wb-service/internal/feature/cardimport/service/contract"
	"io"
	"sync"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_logger "github.com/ERONIS/wb-service/internal/core/logger"
	core_tg_menu "github.com/ERONIS/wb-service/internal/core/transport/telegram/menu"
	cardimport_batch "github.com/ERONIS/wb-service/internal/feature/cardimport/service/batch"
	cardimport_intake "github.com/ERONIS/wb-service/internal/feature/cardimport/service/intake"

	"go.uber.org/zap"
	tele "gopkg.in/telebot.v3"
)

var (
	buttonCards = tele.Btn{
		Text:   "📦 Карточки",
		Unique: "cardimport_menu",
	}
	buttonTransfer = tele.Btn{
		Text:   "📤 Перенос карточек",
		Unique: "cardimport_transfer",
	}
	buttonCancel = tele.Btn{
		Text:   "✖️ Отменить загрузку",
		Unique: "cardimport_cancel",
	}
	buttonFinalize = tele.Btn{
		Text:   "✅ Готово",
		Unique: "cardimport_finalize",
	}
	buttonContinue = tele.Btn{
		Text:   "➡️ Продолжить",
		Unique: "cardimport_continue",
	}
	buttonResumeFiles = tele.Btn{
		Text:   "🔄 Обработать зависшие файлы",
		Unique: "cardimport_resume_files",
	}
)

type CardImportService interface {
	Begin(
		ctx context.Context,
		command cardimport_contract.BeginCommand,
	) (cardimport_intake.Session, error)

	GetActiveView(
		ctx context.Context,
		authorTelegramID int64,
	) (cardimport_intake.SessionView, error)

	GetView(
		ctx context.Context,
		command cardimport_contract.SessionCommand,
	) (cardimport_intake.SessionView, error)

	Cancel(
		ctx context.Context,
		command cardimport_contract.SessionCommand,
	) error

	Continue(
		ctx context.Context,
		command cardimport_contract.ContinueCommand,
	) (cardimport_intake.SessionView, error)

	ReserveFile(
		ctx context.Context,
		command cardimport_contract.ReserveFileCommand,
	) (cardimport_intake.File, error)

	StoreFile(
		ctx context.Context,
		command cardimport_contract.StoreFileCommand,
		source io.Reader,
	) (cardimport_intake.File, error)

	ParseFile(
		ctx context.Context,
		command cardimport_contract.ParseFileCommand,
	) (cardimport_intake.SessionView, error)

	ListRecoverableFiles(
		ctx context.Context,
		staleParsingBefore time.Time,
		reparsePriceErrorsBefore time.Time,
		limit int,
	) ([]cardimport_contract.RecoverableFile, error)

	Finalize(
		ctx context.Context,
		user domain.User,
		command cardimport_contract.FinalizeCommand,
	) (cardimport_batch.BatchHeader, error)
}

type Handler struct {
	ctx                    context.Context
	bot                    *tele.Bot
	service                CardImportService
	completion             CompletionNavigator
	uploadScreenMu         sync.Mutex
	uploadScreenGeneration map[int64]uint64
	menu                   *core_tg_menu.Handler
	logger                 *zap.Logger
	recoveryStartedAt      time.Time
	recoveryStart          sync.Once
	recoveryWake           chan struct{}
	recoveryJobs           chan cardimport_intake.FileID
	recoveryMu             sync.Mutex
	recoveryStates         map[cardimport_intake.FileID]*cardimportRecoveryState
	refreshWake            chan struct{}
	refreshMu              sync.Mutex
	pendingRefreshes       map[int64]cardimportSessionRefresh
}

type CompletionNavigator interface {
	ShowFinalizedSession(
		tele.Context,
		cardimport_batch.BatchHeader,
	) error
}

func New(
	ctx context.Context,
	bot *tele.Bot,
	service CardImportService,
	loggers ...*zap.Logger,
) *Handler {
	if ctx == nil {
		panic("cardimport Telegram context is nil")
	}
	if bot == nil {
		panic("cardimport Telegram bot is nil")
	}
	if service == nil {
		panic("cardimport Telegram service is nil")
	}

	return &Handler{
		ctx:                    ctx,
		bot:                    bot,
		service:                service,
		logger:                 core_logger.Fallback(loggers...),
		recoveryStartedAt:      time.Now(),
		uploadScreenGeneration: make(map[int64]uint64),
		recoveryWake:           make(chan struct{}, 1),
		recoveryJobs:           make(chan cardimport_intake.FileID, 256),
		recoveryStates:         make(map[cardimport_intake.FileID]*cardimportRecoveryState),
		refreshWake:            make(chan struct{}, 1),
		pendingRefreshes:       make(map[int64]cardimportSessionRefresh),
	}
}

func (h *Handler) SetCompletionNavigator(navigator CompletionNavigator) {
	if navigator == nil {
		panic("cardimport completion navigator is nil")
	}
	if h.completion != nil {
		panic("cardimport completion navigator is already configured")
	}
	h.completion = navigator
}

func (h *Handler) Register(menu *core_tg_menu.Handler) {
	if menu == nil {
		panic("cardimport Telegram menu is nil")
	}
	h.menu = menu
	menu.RegisterMenuItem(
		buttonCards,
		domain.RoleAdmin,
		h.showCardsMenu,
	)
	menu.RegisterCallback(
		buttonTransfer,
		domain.RoleAdmin,
		h.startTransfer,
	)
	menu.RegisterCallback(
		buttonCancel,
		domain.RoleAdmin,
		h.cancel,
	)
	menu.RegisterCallback(
		buttonFinalize,
		domain.RoleAdmin,
		h.finalize,
	)
	menu.RegisterCallback(
		buttonContinue,
		domain.RoleAdmin,
		h.continueImport,
	)
	menu.RegisterCallback(
		buttonResumeFiles,
		domain.RoleAdmin,
		h.resumeFiles,
	)
	menu.RegisterDocumentFallback(
		domain.RoleAdmin,
		h.receiveDocument,
	)
	if h.completion == nil {
		panic("cardimport completion navigator is not configured")
	}
	h.recoveryStart.Do(func() {
		go h.runRecoveryScheduler()
		go h.runRecoveryRefreshes()
		for range cardimportRecoveryWorkers {
			go h.runRecoveryWorker()
		}
	})
}
