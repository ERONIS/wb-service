package core_postgres_pool

import (
	"math"
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
	if int64(math.MaxInt) > math.MaxInt32 {
		tooMany := int64(math.MaxInt32) + 1
		invalidMaxConns := valid
		invalidMaxConns.MaxConns = int(tooMany)
		if err := invalidMaxConns.Validate(); err == nil {
			t.Fatal("ожидалась ошибка при MaxConns за пределами int32")
		}
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

func TestPGXPoolConfig(t *testing.T) {
	config := Config{
		Host:     "db.example.com",
		Port:     "5432",
		User:     "app_user",
		Password: "p@ss:w#rd/with%special!chars",
		Database: "service_db",
		SSLMode:  "require",
	}
	for _, test := range []struct {
		name    string
		timeout time.Duration
		want    string
	}{
		{name: "округление", timeout: 1500 * time.Microsecond, want: "2ms"},
		{name: "минимум", timeout: time.Nanosecond, want: "1ms"},
	} {
		t.Run(test.name, func(t *testing.T) {
			config.Timeout = test.timeout
			if err := config.Validate(); err != nil {
				t.Fatalf("проверить конфигурацию: %v", err)
			}
			pgxconfig, err := newPGXPoolConfig(config)
			if err != nil {
				t.Fatalf("собрать конфигурацию пула: %v", err)
			}
			if pgxconfig.ConnConfig.Password != config.Password ||
				pgxconfig.ConnConfig.Host != config.Host ||
				pgxconfig.ConnConfig.Database != config.Database {
				t.Fatalf("учётные данные изменились после разбора DSN")
			}
			if pgxconfig.ConnConfig.TLSConfig == nil {
				t.Fatal("режим require не включил TLS")
			}
			if got := pgxconfig.ConnConfig.RuntimeParams["statement_timeout"]; got != test.want {
				t.Fatalf("statement_timeout=%q, ожидалось %q", got, test.want)
			}
			if got := pgxconfig.ConnConfig.RuntimeParams["application_name"]; got != config.ApplicationName {
				t.Fatalf("application_name=%q, ожидалось %q", got, config.ApplicationName)
			}
		})
	}
}
