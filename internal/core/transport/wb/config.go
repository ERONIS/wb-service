package core_transport_wb

import (
	"fmt"
	"time"

	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	BaseURL string        `envconfig:"BASE_URL" default:"https://content-api.wildberries.ru"`
	Timeout time.Duration `envconfig:"TIMEOUT" default:"20s"`
}

func NewConfig() (Config, error) {
	var config Config

	if err := envconfig.Process("WB_API", &config); err != nil {
		return Config{}, fmt.Errorf(
			"process WB API config: %w",
			err,
		)
	}

	return config, nil
}

func NewConfigMust() Config {
	config, err := NewConfig()
	if err != nil {
		panic(fmt.Errorf(
			"get WB API config: %w",
			err,
		))
	}

	return config
}
