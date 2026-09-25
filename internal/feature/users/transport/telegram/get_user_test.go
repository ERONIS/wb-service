package users_transport_tg

import (
	"testing"

	"github.com/ERONIS/wb-service/internal/core/domain"
)

func TestUserCardMarkupShowsPartnerRoleActions(t *testing.T) {
	tests := []struct {
		name         string
		role         domain.UserRole
		firstButton  string
		expectedRows int
	}{
		{name: "grant partner to user", role: domain.RoleUser, firstButton: buttonSetPartner.Text, expectedRows: 5},
		{name: "revoke partner", role: domain.RolePartner, firstButton: buttonRevokePartner.Text, expectedRows: 5},
		{name: "protect admin", role: domain.RoleAdmin, firstButton: "⬅️ К списку", expectedRows: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			markup := userCardMarkup(domain.User{
				TelegramID: 42,
				Role:       test.role,
			}, 1)
			if len(markup.InlineKeyboard) != test.expectedRows {
				t.Fatalf("rows = %d, want %d", len(markup.InlineKeyboard), test.expectedRows)
			}
			if got := markup.InlineKeyboard[0][0].Text; got != test.firstButton {
				t.Fatalf("first button = %q, want %q", got, test.firstButton)
			}
		})
	}
}
