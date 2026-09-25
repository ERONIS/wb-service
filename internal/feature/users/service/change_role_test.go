package users_service

import (
	"context"
	"errors"
	"testing"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

func TestAdminCanGrantAndRevokePartnerRole(t *testing.T) {
	repository := newRoleRepository(
		domain.User{TelegramID: 1, FullName: "Main Admin", Role: domain.RoleAdmin},
		domain.User{TelegramID: 2, FullName: "Test User", Role: domain.RoleUser},
	)
	service := NewUsersService(repository)

	partner, err := service.SetPartner(context.Background(), 1, 2)
	if err != nil {
		t.Fatalf("SetPartner() error = %v", err)
	}
	if partner.Role != domain.RolePartner || repository.users[2].Role != domain.RolePartner {
		t.Fatalf("SetPartner() user = %+v", partner)
	}

	user, err := service.RevokePartner(context.Background(), 1, 2)
	if err != nil {
		t.Fatalf("RevokePartner() error = %v", err)
	}
	if user.Role != domain.RoleUser || repository.users[2].Role != domain.RoleUser {
		t.Fatalf("RevokePartner() user = %+v", user)
	}
}

func TestPartnerCannotManageRoles(t *testing.T) {
	repository := newRoleRepository(
		domain.User{TelegramID: 1, FullName: "Test Partner", Role: domain.RolePartner},
		domain.User{TelegramID: 2, FullName: "Test User", Role: domain.RoleUser},
	)
	service := NewUsersService(repository)

	_, err := service.SetPartner(context.Background(), 1, 2)
	if !errors.Is(err, core_errors.ErrForbidden) {
		t.Fatalf("SetPartner() error = %v, want ErrForbidden", err)
	}
	if repository.users[2].Role != domain.RoleUser {
		t.Fatal("unauthorized role change was persisted")
	}
}

func TestAdminRoleCannotBeRevokedThroughPartnerActions(t *testing.T) {
	repository := newRoleRepository(
		domain.User{TelegramID: 1, FullName: "First Admin", Role: domain.RoleAdmin},
		domain.User{TelegramID: 2, FullName: "Other Admin", Role: domain.RoleAdmin},
	)
	service := NewUsersService(repository)

	for name, operation := range map[string]func() error{
		"set partner": func() error {
			_, err := service.SetPartner(context.Background(), 1, 2)
			return err
		},
		"revoke partner": func() error {
			_, err := service.RevokePartner(context.Background(), 1, 2)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := operation(); !errors.Is(err, core_errors.ErrForbidden) {
				t.Fatalf("operation error = %v, want ErrForbidden", err)
			}
		})
	}
	if repository.users[2].Role != domain.RoleAdmin {
		t.Fatal("admin role was changed")
	}
}

type roleRepository struct {
	users map[int64]domain.User
}

func newRoleRepository(users ...domain.User) *roleRepository {
	repository := &roleRepository{users: make(map[int64]domain.User, len(users))}
	for _, user := range users {
		repository.users[user.TelegramID] = user
	}
	return repository
}

func (repository *roleRepository) SaveUser(
	_ context.Context,
	user domain.User,
) (domain.User, error) {
	repository.users[user.TelegramID] = user
	return user, nil
}

func (repository *roleRepository) GetUserByTelegramID(
	_ context.Context,
	telegramID int64,
) (domain.User, error) {
	user, found := repository.users[telegramID]
	if !found {
		return domain.User{}, core_errors.ErrNotFound
	}
	return user, nil
}

func (repository *roleRepository) UpdateUser(
	_ context.Context,
	user domain.User,
) (domain.User, error) {
	if _, found := repository.users[user.TelegramID]; !found {
		return domain.User{}, core_errors.ErrNotFound
	}
	repository.users[user.TelegramID] = user
	return user, nil
}

func (repository *roleRepository) DeleteUser(
	_ context.Context,
	telegramID int64,
) error {
	if _, found := repository.users[telegramID]; !found {
		return core_errors.ErrNotFound
	}
	delete(repository.users, telegramID)
	return nil
}

func (repository *roleRepository) GetUsers(
	_ context.Context,
	limit int,
	offset int,
) ([]domain.User, error) {
	result := make([]domain.User, 0, len(repository.users))
	for _, user := range repository.users {
		result = append(result, user)
	}
	if offset >= len(result) {
		return nil, nil
	}
	result = result[offset:]
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

var _ UsersRepository = (*roleRepository)(nil)
