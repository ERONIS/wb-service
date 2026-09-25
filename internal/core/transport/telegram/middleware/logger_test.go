package core_tg_middleware

import (
	"strings"
	"testing"
)

func TestRedactTelegramBotTokens(t *testing.T) {
	t.Parallel()

	const token = "123456:secret-value"
	message := `Post "https://api.telegram.org/bot` + token + `/editMessageText": timeout`
	redacted := redactTelegramBotTokens(message)
	if strings.Contains(redacted, token) {
		t.Fatalf("redacted message still contains token: %q", redacted)
	}
	if !strings.Contains(redacted, "/bot<redacted>/editMessageText") {
		t.Fatalf("redacted message = %q", redacted)
	}
}
