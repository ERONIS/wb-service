package core_postgres_pool

import (
	"net"
	"net/url"
	"testing"
	"time"
)

func TestConfigValidate(t *testing.T) {
	valid := Config{
		Host:              "localhost",
		Port:              "5432",
		User:              "user",
		Password:          "pass",
		Database:          "test_db",
		SSLMode:           "disable",
		Timeout:           5 * time.Second,
		MaxConns:          25,
		MinConns:          4,
		MaxConnLifetime:   time.Hour,
		MaxConnIdleTime:   30 * time.Minute,
		HealthCheckPeriod: time.Minute,
	}

	if err := valid.Validate(); err != nil {
		t.Fatalf("ожидалась валидная конфигурация, получена ошибка: %v", err)
	}
	if valid.ApplicationName != "wb-service" {
		t.Fatalf("неверное имя приложения по умолчанию: %q", valid.ApplicationName)
	}

	// Проверка MinConns > MaxConns
	invalidMinConns := valid
	invalidMinConns.MinConns = 30
	if err := invalidMinConns.Validate(); err == nil {
		t.Fatal("ожидалась ошибка при MinConns > MaxConns, получен nil")
	}

	// Проверка неверного sslmode
	invalidSSL := valid
	invalidSSL.SSLMode = "invalid_ssl"
	if err := invalidSSL.Validate(); err == nil {
		t.Fatal("ожидалась ошибка при неверном sslmode, получен nil")
	}

	// Проверка пустого хоста
	emptyHost := valid
	emptyHost.Host = ""
	if err := emptyHost.Validate(); err == nil {
		t.Fatal("ожидалась ошибка при пустом хосте, получен nil")
	}

	invalidApplicationName := valid
	invalidApplicationName.ApplicationName = "name\nsecret"
	if err := invalidApplicationName.Validate(); err == nil {
		t.Fatal("ожидалась ошибка при недопустимом имени приложения")
	}
}

func TestDSNEscapingSpecialCharacters(t *testing.T) {
	config := Config{
		Host:     "db.example.com",
		Port:     "5432",
		User:     "app_user",
		Password: "p@ss:w#rd/with%special!chars",
		Database: "service_db",
		SSLMode:  "require",
	}

	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(config.User, config.Password),
		Host:   net.JoinHostPort(config.Host, config.Port),
		Path:   config.Database,
	}
	query := u.Query()
	query.Set("sslmode", config.SSLMode)
	u.RawQuery = query.Encode()

	dsn := u.String()

	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("ошибка разбора сгенерированного DSN: %v", err)
	}

	password, ok := parsed.User.Password()
	if !ok {
		t.Fatal("пароль отсутствует в разобранном URL")
	}
	if password != config.Password {
		t.Fatalf("пароль не совпадает после декодирования: получено %q, ожидалось %q", password, config.Password)
	}
	if parsed.Query().Get("sslmode") != "require" {
		t.Fatalf("sslmode не совпадает: получено %q, ожидалось require", parsed.Query().Get("sslmode"))
	}
}
