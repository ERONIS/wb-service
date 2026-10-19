package core_tg_ui

import (
	"strconv"
	"strings"

	tele "gopkg.in/telebot.v3"
)

// PaginationConfig задаёт конфигурацию для построения ряда кнопок пагинации.
type PaginationConfig struct {
	// Page — текущая страница (начиная с 1).
	Page int
	// TotalPages — общее количество страниц.
	TotalPages int
	// CallbackUnique — уникальный идентификатор callback-кнопки переключения страницы.
	CallbackUnique string
	// ArgPrefix — аргументы, передаваемые перед номером страницы.
	ArgPrefix []string
	// ArgSuffix — аргументы, передаваемые после номера страницы.
	ArgSuffix []string
	// PrevText — текст кнопки перехода назад. Если не указан, используется «⬅️ Назад».
	PrevText string
	// NextText — текст кнопки перехода вперёд. Если не указан, используется «Вперёд ➡️».
	NextText string
	// PageEncoder — функция кодирования номера страницы в callback_data. По умолчанию strconv.Itoa.
	PageEncoder func(page int) string
}

// CalculateTotalPages вычисляет общее количество страниц по размеру страницы и общему числу элементов.
func CalculateTotalPages(totalItems, pageSize int64) int64 {
	if totalItems <= 0 || pageSize <= 0 {
		return 1
	}
	return (totalItems + pageSize - 1) / pageSize
}

// BuildPaginationRow строит ряд кнопок навигации (Назад / Вперёд) для inline-клавиатуры.
// Если страниц <= 1, возвращает пустой ряд и false.
func BuildPaginationRow(markup *tele.ReplyMarkup, cfg PaginationConfig) (tele.Row, bool) {
	if markup == nil || cfg.TotalPages <= 1 || cfg.CallbackUnique == "" {
		return nil, false
	}

	encoder := cfg.PageEncoder
	if encoder == nil {
		encoder = strconv.Itoa
	}

	prevText := strings.TrimSpace(cfg.PrevText)
	if prevText == "" {
		prevText = "⬅️ Назад"
	}

	nextText := strings.TrimSpace(cfg.NextText)
	if nextText == "" {
		nextText = "Вперёд ➡️"
	}

	buildArgs := func(targetPage int) []string {
		args := make([]string, 0, len(cfg.ArgPrefix)+1+len(cfg.ArgSuffix))
		args = append(args, cfg.ArgPrefix...)
		args = append(args, encoder(targetPage))
		args = append(args, cfg.ArgSuffix...)
		return args
	}

	buttons := make([]tele.Btn, 0, 2)
	if cfg.Page > 1 {
		buttons = append(buttons, markup.Data(
			prevText,
			cfg.CallbackUnique,
			buildArgs(cfg.Page-1)...,
		))
	}
	if cfg.Page < cfg.TotalPages {
		buttons = append(buttons, markup.Data(
			nextText,
			cfg.CallbackUnique,
			buildArgs(cfg.Page+1)...,
		))
	}

	if len(buttons) == 0 {
		return nil, false
	}

	return markup.Row(buttons...), true
}
