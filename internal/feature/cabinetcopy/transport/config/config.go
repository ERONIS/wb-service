package cabinetcopy_config_transport

import (
	"fmt"

	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	MaxCards int `envconfig:"MAX_CARDS" default:"1000"`
}

func New() (Config, error) {
	var config Config
	if err := envconfig.Process("CABINET_COPY", &config); err != nil {
		return Config{}, fmt.Errorf("process cabinet copy config: %w", err)
	}
	if err := config.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate cabinet copy config: %w", err)
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
	if config.MaxCards <= 0 || config.MaxCards > 50000 {
		return fmt.Errorf("max cards must be between 1 and 50000")
	}
	return nil
}
