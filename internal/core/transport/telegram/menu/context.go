package core_tg_menu

import (
	tele "gopkg.in/telebot.v3"
)

func isUserMessage(ctx tele.Context) bool {
	if ctx == nil {
		return false
	}
	message := ctx.Update().Message
	return message != nil && message.Sender != nil && !message.Sender.IsBot
}

func inputMessageID(ctx tele.Context) int {
	if !isUserMessage(ctx) {
		return 0
	}
	return ctx.Update().Message.ID
}

type menuContext struct {
	tele.Context
	host              *menuHost
	repositionOnInput bool
	inputMessageID    int
	inputDeleted      bool
	repositioned      bool
	responded         bool
}

// Respond отправляет ответ на callback один раз за обработку обновления.
func (ctx *menuContext) Respond(resp ...*tele.CallbackResponse) error {
	if ctx.responded {
		return nil
	}
	if err := ctx.Context.Respond(resp...); err != nil {
		return err
	}
	ctx.responded = true
	return nil
}

// RespondText подтверждает callback текстом без повторного запроса.
func (ctx *menuContext) RespondText(text string) error {
	return ctx.Respond(&tele.CallbackResponse{Text: text})
}

// RespondAlert подтверждает callback всплывающим сообщением.
func (ctx *menuContext) RespondAlert(text string) error {
	return ctx.Respond(&tele.CallbackResponse{Text: text, ShowAlert: true})
}

func (ctx *menuContext) Send(what interface{}, opts ...interface{}) error {
	return ctx.render(what, opts...)
}

func (ctx *menuContext) Edit(what interface{}, opts ...interface{}) error {
	return ctx.render(what, opts...)
}

func (ctx *menuContext) EditOrSend(what interface{}, opts ...interface{}) error {
	return ctx.render(what, opts...)
}

func (ctx *menuContext) Delete() error {
	err := ctx.Context.Delete()
	if err == nil && ctx.repositionOnInput {
		ctx.inputDeleted = true
	}
	return err
}

func (ctx *menuContext) SendNewMenu(what interface{}, opts ...interface{}) error {
	text, ok := what.(string)
	if !ok {
		return ctx.Context.Send(what, opts...)
	}
	err := ctx.host.render(ctx.Context, menuPosition{fresh: true}, text, opts...)
	if err == nil {
		ctx.repositioned = true
	}
	return err
}

func (ctx *menuContext) NotifyMenu(key string, text string) error {
	return ctx.host.notify(ctx, key, text)
}

func (ctx *menuContext) position() menuPosition {
	if ctx.repositionOnInput && !ctx.repositioned && !ctx.inputDeleted {
		return menuPosition{afterMessageID: ctx.inputMessageID}
	}
	return menuPosition{}
}

func (ctx *menuContext) render(what interface{}, opts ...interface{}) error {
	text, ok := what.(string)
	if !ok {
		return ctx.Context.Send(what, opts...)
	}
	err := ctx.host.render(ctx.Context, ctx.position(), text, opts...)
	if err == nil {
		ctx.repositioned = true
	}
	return err
}

func (ctx *menuContext) ensureMenuAfterInput() error {
	if !ctx.repositionOnInput || ctx.repositioned {
		return nil
	}

	repositioned, err := ctx.host.renderCached(ctx.Context, ctx.position())
	if err != nil {
		return err
	}
	if repositioned {
		ctx.repositioned = true
		return nil
	}

	fallback := ctx.host.inputFallback()
	if fallback == nil {
		return nil
	}
	if err := fallback(ctx); err != nil {
		return err
	}
	ctx.repositioned = true
	return nil
}

var _ tele.Context = (*menuContext)(nil)
