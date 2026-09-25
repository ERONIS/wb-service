package cardedit_config_transport

import (
	"fmt"
	"time"

	cardedit_service "github.com/ERONIS/wb-service/internal/feature/cardedit/service"

	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	FlowTTL      time.Duration `envconfig:"FLOW_TTL" default:"15m"`
	PollInterval time.Duration `envconfig:"POLL_INTERVAL" default:"30s"`
	MaxAttempts  int           `envconfig:"MAX_ATTEMPTS" default:"4"`
	Workers      int           `envconfig:"WORKERS" default:"10"`
	QueueSize    int           `envconfig:"QUEUE_SIZE" default:"40"`
}

func New() (Config, error) {
	var config Config
	if err := envconfig.Process("CARD_EDIT", &config); err != nil {
		return Config{}, fmt.Errorf("process card edit config: %w", err)
	}
	if err := config.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate card edit config: %w", err)
	}
	return config, nil
}

func Must() Config {
	config, err := New()
	if err != nil {
		panic(err)
	}
	return config
}

func (config Config) Validate() error {
	if config.FlowTTL <= 0 || config.FlowTTL > 24*time.Hour {
		return fmt.Errorf("flow TTL must be between zero and 24 hours")
	}
	return config.ServiceConfig().Validate()
}

func (config Config) ServiceConfig() cardedit_service.Config {
	return cardedit_service.Config{
		PollInterval: config.PollInterval,
		MaxAttempts:  config.MaxAttempts,
		Workers:      config.Workers,
		QueueSize:    config.QueueSize,
	}
}
