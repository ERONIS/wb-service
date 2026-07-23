package core_postgres_pool

import (
	"fmt"
	"time"

	"github.com/kelseyhightower/envconfig"
)

// Config хранит параметры подключения к PostgreSQL.
// Все поля читаются из переменных окружения с префиксом "POSTGRES_":
// POSTGRES_HOST, POSTGRES_PORT, POSTGRES_USER, POSTGRES_PASSWORD, POSTGRES_DB, POSTGRES_TIMEOUT.
type Config struct {
	Host     string `envconfig:"HOST"     required:"true"`
	Port     string `envconfig:"PORT"     default:"5432"`
	User     string `envconfig:"USER"     required:"true"`
	Password string `envconfig:"PASSWORD" required:"true"`
	Database string `envconfig:"DB"       required:"true"`

	// Timeout — максимальное время выполнения одного запроса к базе данных.
	// Используется репозиториями через Pool.OpTimeout() + context.WithTimeout.
	Timeout time.Duration `envconfig:"TIMEOUT"  required:"true"`
}

// NewConfig читает конфигурацию пула из переменных окружения.
func NewConfig() (Config, error) {
	var config Config

	if err := envconfig.Process("POSTGRES", &config); err != nil {
		return Config{}, fmt.Errorf("process envconfig: %w", err)
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
