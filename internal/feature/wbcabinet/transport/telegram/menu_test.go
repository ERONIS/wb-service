package wbcabinet_telegram_transport

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	wbcabinet_service "github.com/ERONIS/wb-service/internal/feature/wbcabinet/service"
)

func TestProfileTextIncludesEscapedUserCabinetAndAPIPermissions(t *testing.T) {
	text := profileText(
		domain.User{
			TelegramID: 42,
			FullName:   "Иван <ИП>",
			Role:       domain.RoleUser,
		},
		"@seller&shop",
		[]wbcabinet_service.Cabinet{{
			Name: "Основной <WB>",
			TokenProperties: wbcabinet_service.PermissionContent |
				wbcabinet_service.PermissionAnalytics |
				wbcabinet_service.PermissionPrices,
			CredentialExpiresAt: time.Now().Add(time.Hour),
			Status:              wbcabinet_service.StatusActive,
		}},
		"",
	)
	for _, want := range []string{
		"Иван &lt;ИП&gt;",
		"@seller&amp;shop",
		"Основной &lt;WB&gt;",
		"Контент, Аналитика, Цены и скидки",
		"чтение и запись",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("profileText() does not contain %q:\n%s", want, text)
		}
	}
}

func TestCabinetCallbackKeyIsShortAndValidated(t *testing.T) {
	id := wbcabinet_service.CabinetID("cabinet-with-a-private-durable-identifier")
	key := cabinetCallbackKey(id)
	if strings.Contains(key, string(id)) || len(key) != 16 {
		t.Fatalf("cabinetCallbackKey() = %q", key)
	}
	parsed, err := parseCabinetCallbackKey([]string{key})
	if err != nil || parsed != key {
		t.Fatalf("parseCabinetCallbackKey() = %q, %v", parsed, err)
	}
	if _, err := parseCabinetCallbackKey([]string{"invalid"}); err == nil {
		t.Fatal("parseCabinetCallbackKey() accepted invalid key")
	}
}

func TestAccessModeTextRecognizesReadOnlyToken(t *testing.T) {
	properties := wbcabinet_service.PermissionContent |
		wbcabinet_service.PermissionReadOnly
	if got := accessModeText(properties); got != "только чтение" {
		t.Fatalf("accessModeText() = %q", got)
	}
}

func TestServiceErrorTextPrefersVerificationRateLimit(t *testing.T) {
	err := errors.Join(
		wbcabinet_service.ErrVerification,
		wbcabinet_service.ErrVerificationRateLimited,
	)
	if got := serviceErrorText(err); !strings.Contains(got, "Подождите 30 секунд") {
		t.Fatalf("serviceErrorText() = %q", got)
	}
}
