package cards_transport_tg

import (
	"context"
	"io"
	"sync"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	cards_service "github.com/ERONIS/wb-service/internal/feature/cards/service"
	tele "gopkg.in/telebot.v3"
)

var (
	buttonCardsMenu = tele.Btn{
		Text:   "📂 Карточки",
		Unique: "cards_menu",
	}
	buttonTransferCards = tele.Btn{
		Text:   "📤 Перенос карточек",
		Unique: "cards_transfer",
	}
	buttonEditCards = tele.Btn{
		Text:   "✏️ Редактирование карточек",
		Unique: "cards_edit",
	}
)

type CardsService interface {
	CanImportCards(
		ctx context.Context,
		telegramID int64,
	) (bool, error)

	ImportCards(
		ctx context.Context,
		telegramID int64,
		purpose domain.CardImportPurpose,
		file cards_service.ImportFile,
		reader io.Reader,
	) (domain.CardImport, error)
}

type CardsTgHandler struct {
	ctx               context.Context
	cardsService      CardsService
	openFile          func(*tele.File) (io.ReadCloser, error)
	SelectedPurposeMu sync.Mutex
	SelectedPurpose   map[int64]domain.CardImportPurpose
}

func NewCardsTgHandler(
	ctx context.Context,
	cardsService CardsService,
) *CardsTgHandler {
	return &CardsTgHandler{
		ctx:             ctx,
		cardsService:    cardsService,
		SelectedPurpose: make(map[int64]domain.CardImportPurpose),
	}
}
func (h *CardsTgHandler) Register(
	bot *tele.Bot,
	menu *core_transport_telegram.Handler,
) {
	h.openFile = bot.File

	menu.RegisterMainMenu()
}
