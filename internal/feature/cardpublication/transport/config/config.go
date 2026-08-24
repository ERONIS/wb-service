package cardpublication_config_transport

import (
	"fmt"
	"time"

	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	ReconciliationDelay   time.Duration `envconfig:"RECONCILIATION_DELAY" default:"10s"`
	ReconciliationTimeout time.Duration `envconfig:"RECONCILIATION_TIMEOUT" default:"30m"`
	MediaAutoDispatch     bool          `envconfig:"MEDIA_AUTO_DISPATCH" default:"true"`
}

func New() (Config, error) {
	var config Config
	if err := envconfig.Process("CARDPUBLICATION", &config); err != nil {
		return Config{}, fmt.Errorf("process cardpublication config: %w", err)
	}
	if err := config.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate cardpublication config: %w", err)
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
	if config.ReconciliationDelay <= 0 {
		return fmt.Errorf("reconciliation delay must be positive")
	}
	if config.ReconciliationTimeout <= config.ReconciliationDelay ||
		config.ReconciliationTimeout > 24*time.Hour {
		return fmt.Errorf("reconciliation timeout must be greater than delay and at most 24 hours")
	}
	return nil
}
