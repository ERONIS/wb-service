package core_tg_server

import (
	"fmt"
	"time"

	"github.com/kelseyhightower/envconfig"
)

// Config содержит только инфраструктурные настройки Telegram-бота.
type Config struct {
	Token         string        `envconfig:"TOKEN" required:"true"`
	PollTimeout   time.Duration `envconfig:"POLL_TIMEOUT" default:"30s"`
	HTTPTimeout   time.Duration `envconfig:"HTTP_TIMEOUT" default:"40s"`
	UpdatesBuffer int           `envconfig:"UPDATES_BUFFER" default:"100"`
	Synchronous   bool          `envconfig:"SYNCHRONOUS" default:"false"`
	Verbose       bool          `envconfig:"VERBOSE" default:"false"`
	Offline       bool          `envconfig:"OFFLINE" default:"false"`
}

func NewConfig() (Config, error) {
	var cfg Config

	if err := envconfig.Process("TELEGRAM", &cfg); err != nil {
		return Config{}, fmt.Errorf("process telegram config: %w", err)
	}

	return cfg, nil
}

func NewConfigMust() Config {
	cfg, err := NewConfig()
	if err != nil {
		panic(fmt.Errorf("get Telegram config: %w", err))
	}

	return cfg
}
