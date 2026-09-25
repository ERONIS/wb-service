package core_transport_telegram

import (
	"errors"

	tele "gopkg.in/telebot.v3"
)

type ErrorCase struct {
	Target error
	Key    string
	Text   string
}

func OnError(target error, key, text string) ErrorCase {
	return ErrorCase{Target: target, Key: key, Text: text}
}

func MatchError(
	err error,
	fallback ErrorCase,
	cases ...ErrorCase,
) ErrorCase {
	for _, item := range cases {
		if item.Target != nil && errors.Is(err, item.Target) {
			return item
		}
	}
	return fallback
}

func NotifyError(
	ctx tele.Context,
	err error,
	fallback ErrorCase,
	cases ...ErrorCase,
) error {
	notification := MatchError(err, fallback, cases...)
	return Notify(ctx, notification.Key, notification.Text)
}
