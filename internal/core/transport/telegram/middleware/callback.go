package core_tg_middleware

import (
	tele "gopkg.in/telebot.v3"
	tele_middleware "gopkg.in/telebot.v3/middleware"
)

// Callback добавляет автоматический ответ после middleware фичи.
func Callback(
	featureMiddleware ...tele.MiddlewareFunc,
) []tele.MiddlewareFunc {
	chain := append(
		[]tele.MiddlewareFunc(nil),
		featureMiddleware...,
	)

	return append(chain, tele_middleware.AutoRespond())
}
