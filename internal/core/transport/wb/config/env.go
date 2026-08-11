package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
)

const (
	envCabinets      = "WB_API_CABINETS"
	envCabinetPrefix = "WB_API_CABINET_"
)

type environmentConfig struct {
	BaseURL string `envconfig:"BASE_URL" default:"https://content-api.wildberries.ru"`

	Timeout time.Duration `envconfig:"TIMEOUT" default:"20s"`
}

func NewConfig() (Config, error) {
	var environment environmentConfig

	if err := envconfig.Process("WB_API", &environment); err != nil {
		return Config{}, fmt.Errorf(
			"process WB API config: %w",
			err,
		)
	}

	cabinets, err := loadCabinetsFromEnv()
	if err != nil {
		return Config{}, fmt.Errorf(
			"load WB API cabinets: %w",
			err,
		)
	}

	config := Config{
		BaseURL:  strings.TrimSpace(environment.BaseURL),
		Timeout:  environment.Timeout,
		Cabinets: cabinets,
	}

	if err := config.Validate(); err != nil {
		return Config{}, fmt.Errorf(
			"validate WB API config: %w",
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

func loadCabinetsFromEnv() ([]CabinetConfig, error) {
	rawCabinetIDs, exists := os.LookupEnv(envCabinets)
	if !exists || strings.TrimSpace(rawCabinetIDs) == "" {
		return nil, fmt.Errorf("WB API cabinet list is empty")
	}

	rawIDs := strings.Split(rawCabinetIDs, ",")
	cabinets := make([]CabinetConfig, 0, len(rawIDs))

	for index, rawID := range rawIDs {
		id := CabinetID(strings.TrimSpace(rawID))
		if id == "" {
			return nil, fmt.Errorf(
				"WB API cabinet ID at position %d is empty",
				index,
			)
		}

		suffix := strings.ToUpper(string(id))

		name, exists := os.LookupEnv(
			envCabinetPrefix + suffix + "_NAME",
		)
		if !exists {
			return nil, fmt.Errorf(
				"WB API cabinet %q name is missing",
				id,
			)
		}

		token, exists := os.LookupEnv(
			envCabinetPrefix + suffix + "_TOKEN",
		)
		if !exists {
			return nil, fmt.Errorf(
				"WB API cabinet %q token is missing",
				id,
			)
		}

		cabinets = append(cabinets, CabinetConfig{
			ID:    id,
			Name:  strings.TrimSpace(name),
			Token: token,
		})
	}

	return cabinets, nil
}
