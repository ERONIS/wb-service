package users_postgres_row

import (
	"reflect"
	"testing"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"

	"github.com/jackc/pgx/v5"
)

// mockRow реализует интерфейс pgx.Row для модульных тестов без БД.
type mockRow struct {
	values []any
}

func (m *mockRow) Scan(dest ...any) error {
	for i, d := range dest {
		switch target := d.(type) {
		case *int64:
			*target = m.values[i].(int64)
		case **string:
			if m.values[i] == nil {
				*target = nil
			} else {
				val := m.values[i].(string)
				*target = &val
			}
		case *string:
			*target = m.values[i].(string)
		case *domain.UserRole:
			*target = m.values[i].(domain.UserRole)
		case *time.Time:
			*target = m.values[i].(time.Time)
		}
	}
	return nil
}

func TestUserRowDTO_ScanAndDomainMapping(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	nick := "test_nick"

	row := &mockRow{
		values: []any{
			int64(42),
			int64(123456789),
			nick,
			"Иван Иванов",
			domain.RolePartner,
			now,
			now.Add(time.Hour),
		},
	}

	var dto Row
	if err := dto.Scan(row); err != nil {
		t.Fatalf("неожиданная ошибка Scan: %v", err)
	}

	if dto.ID != 42 {
		t.Errorf("ожидался ID 42, получено %d", dto.ID)
	}
	if dto.TelegramID != 123456789 {
		t.Errorf("ожидался TelegramID 123456789, получено %d", dto.TelegramID)
	}
	if dto.TelegramNickname == nil || *dto.TelegramNickname != nick {
		t.Errorf("ожидался ник %s, получено %v", nick, dto.TelegramNickname)
	}
	if dto.Role != domain.RolePartner {
		t.Errorf("ожидалась роль %s, получено %s", domain.RolePartner, dto.Role)
	}

	u := ToDomain(dto)
	if u.ID != 42 || u.TelegramID != 123456789 || u.Role != domain.RolePartner {
		t.Errorf("некорректный маппинг в домен: %+v", u)
	}
}

func TestUserRowDTO_RowToStructByName_Compilation(t *testing.T) {
	// Проверяем, что функция pgx.RowToStructByName валидна для Row на этапе компиляции
	collector := pgx.RowToStructByName[Row]
	if collector == nil {
		t.Fatal("RowToStructByName вернул nil")
	}

	// Проверяем теги db у структуры Row
	val := reflect.TypeOf(Row{})
	expectedTags := map[string]string{
		"ID":               "id",
		"TelegramID":       "tg_id",
		"TelegramNickname": "tg_nickname",
		"FullName":         "full_name",
		"Role":             "role",
		"CreatedAt":        "created_at",
		"UpdatedAt":        "updated_at",
	}

	for fieldName, expectedTag := range expectedTags {
		field, ok := val.FieldByName(fieldName)
		if !ok {
			t.Fatalf("поле %s не найдено в Row", fieldName)
		}
		if tag := field.Tag.Get("db"); tag != expectedTag {
			t.Errorf("поле %s имеет db-тег '%s', ожидался '%s'", fieldName, tag, expectedTag)
		}
	}
}

func BenchmarkUserRowDTO_Mapping(b *testing.B) {
	now := time.Now().UTC()
	nick := "test_nick"
	dto := Row{
		ID:               100,
		TelegramID:       987654321,
		TelegramNickname: &nick,
		FullName:         "Тестовый Пользователь",
		Role:             domain.RoleAdmin,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = ToDomain(dto)
	}
}
