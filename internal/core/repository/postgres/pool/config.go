package core_postgres_pool

import (
	"errors"
	"fmt"
	"time"

	"github.com/kelseyhightower/envconfig"
)

// Config хранит параметры подключения и настройки пула соединений PostgreSQL.
// Все поля читаются из переменных окружения с префиксом "POSTGRES_":
// POSTGRES_HOST, POSTGRES_PORT, POSTGRES_USER, POSTGRES_PASSWORD, POSTGRES_DB, POSTGRES_TIMEOUT,
// POSTGRES_SSLMODE, POSTGRES_MAX_CONNS, POSTGRES_MIN_CONNS, POSTGRES_MAX_CONN_LIFETIME,
// POSTGRES_MAX_CONN_IDLE_TIME, POSTGRES_HEALTHCHECK_PERIOD, POSTGRES_APPLICATION_NAME.
type Config struct {
	Host     string `envconfig:"HOST"     required:"true"`
	Port     string `envconfig:"PORT"     default:"5432"`
	User     string `envconfig:"USER"     required:"true"`
	Password string `envconfig:"PASSWORD" required:"true"`
	Database string `envconfig:"DB"       required:"true"`

	// ApplicationName позволяет отличать соединения сервиса в статистике PostgreSQL.
	ApplicationName string `envconfig:"APPLICATION_NAME" default:"wb-service"`

	// SSLMode определяет режим TLS соединения (disable, require, verify-ca, verify-full).
	SSLMode string `envconfig:"SSLMODE" default:"disable"`

	// Timeout — максимальное время выполнения одного запроса к базе данных по умолчанию.
	// Используется репозиториями через Pool.OperationContext().
	Timeout time.Duration `envconfig:"TIMEOUT" required:"true"`

	// MaxConns — максимальное количество соединений в пуле (по умолчанию 25).
	MaxConns int `envconfig:"MAX_CONNS" default:"25"`

	// MinConns — минимальное количество активных соединений в пуле (по умолчанию 4).
	MinConns int `envconfig:"MIN_CONNS" default:"4"`

	// MaxConnLifetime — максимальное время жизни соединения в пуле (по умолчанию 1h).
	MaxConnLifetime time.Duration `envconfig:"MAX_CONN_LIFETIME" default:"1h"`

	// MaxConnIdleTime — максимальное время простоя свободного соединения (по умолчанию 30m).
	MaxConnIdleTime time.Duration `envconfig:"MAX_CONN_IDLE_TIME" default:"30m"`

	// HealthCheckPeriod — интервал фоновой проверки доступности соединений в пуле (по умолчанию 1m).
	HealthCheckPeriod time.Duration `envconfig:"HEALTHCHECK_PERIOD" default:"1m"`
}

// Validate проверяет корректность параметров конфигурации пула и заполняет значения по умолчанию.
func (c *Config) Validate() error {
	if c.Host == "" {
		return errors.New("хост PostgreSQL не может быть пустым")
	}
	if c.Port == "" {
		c.Port = "5432"
	}
	if c.User == "" {
		return errors.New("пользователь PostgreSQL не может быть пустым")
	}
	if c.Password == "" {
		return errors.New("пароль PostgreSQL не может быть пустым")
	}
	if c.Database == "" {
		return errors.New("имя базы данных PostgreSQL не может быть пустым")
	}
	if c.ApplicationName == "" {
		c.ApplicationName = "wb-service"
	}
	if len(c.ApplicationName) > 63 {
		return errors.New("имя приложения PostgreSQL длиннее 63 байт")
	}
	for _, character := range c.ApplicationName {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' || character == '_' || character == '.' {
			continue
		}
		return errors.New("имя приложения PostgreSQL содержит недопустимые символы")
	}
	if c.Timeout <= 0 {
		return errors.New("таймаут PostgreSQL должен быть положительным")
	}
	if c.SSLMode == "" {
		c.SSLMode = "disable"
	}
	switch c.SSLMode {
	case "disable", "require", "verify-ca", "verify-full":
		// Корректный режим SSL
	default:
		return fmt.Errorf("неподдерживаемый режим sslmode PostgreSQL: %q", c.SSLMode)
	}
	if c.MaxConns <= 0 {
		c.MaxConns = 25
	}
	if c.MinConns < 0 {
		return errors.New("минимальное число соединений PostgreSQL не может быть отрицательным")
	}
	if c.MinConns == 0 {
		c.MinConns = 4
	}
	if c.MinConns > c.MaxConns {
		return fmt.Errorf(
			"минимальное число соединений PostgreSQL (%d) не может превышать максимальное (%d)",
			c.MinConns,
			c.MaxConns,
		)
	}
	if c.MaxConnLifetime <= 0 {
		c.MaxConnLifetime = time.Hour
	}
	if c.MaxConnIdleTime <= 0 {
		c.MaxConnIdleTime = 30 * time.Minute
	}
	if c.HealthCheckPeriod <= 0 {
		c.HealthCheckPeriod = time.Minute
	}
	return nil
}

// NewConfig читает конфигурацию пула из переменных окружения и проверяет её.
func NewConfig() (Config, error) {
	var config Config

	if err := envconfig.Process("POSTGRES", &config); err != nil {
		return Config{}, fmt.Errorf("process envconfig: %w", err)
	}
	if err := config.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate config: %w", err)
	}

	return config, nil
}

// NewConfigMust — «Must»-вариант конструктора: паникует при ошибке.
func NewConfigMust() Config {
	config, err := NewConfig()
	if err != nil {
		err = fmt.Errorf("get Postgres connection pool config: %w", err)
		panic(err)
	}

	return config
}
