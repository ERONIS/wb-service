package transfer_config_transport

import (
	"fmt"
	"strings"
	"time"

	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"

	"github.com/kelseyhightower/envconfig"
)

type Mode string

const (
	ModeDisabled Mode = "disabled"
	ModeDryRun   Mode = "dry-run"
	ModeLive     Mode = "live"
)

type Config struct {
	Mode            Mode          `envconfig:"MODE" default:"disabled"`
	CohortName      string        `envconfig:"COHORT_NAME" default:"production"`
	PollInterval    time.Duration `envconfig:"POLL_INTERVAL" default:"3s"`
	MaxItems        int64         `envconfig:"MAX_ITEMS" default:"50000"`
	MaxGroups       int64         `envconfig:"MAX_GROUPS" default:"50000"`
	MaxTargets      int64         `envconfig:"MAX_TARGETS" default:"20"`
	MaxItemTargets  int64         `envconfig:"MAX_ITEM_TARGETS" default:"1000000"`
	MaxGroupTargets int64         `envconfig:"MAX_GROUP_TARGETS" default:"1000000"`
}

func New() (Config, error) {
	var config Config
	if err := envconfig.Process("TRANSFER", &config); err != nil {
		return Config{}, fmt.Errorf("process transfer config: %w", err)
	}
	config.CohortName = strings.TrimSpace(config.CohortName)
	if err := config.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate transfer config: %w", err)
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
	switch config.Mode {
	case ModeDisabled, ModeDryRun, ModeLive:
	default:
		return fmt.Errorf("unsupported mode %q", config.Mode)
	}
	if strings.TrimSpace(config.CohortName) != config.CohortName ||
		config.CohortName == "" || len(config.CohortName) > 128 {
		return fmt.Errorf("cohort name is invalid")
	}
	if config.PollInterval <= 0 {
		return fmt.Errorf("poll interval must be positive")
	}
	return config.CapacityPolicy().Validate()
}

func (config Config) CapacityPolicy() transfer_service.CapacityPolicy {
	return transfer_service.CapacityPolicy{
		MaxItems:        config.MaxItems,
		MaxGroups:       config.MaxGroups,
		MaxTargets:      config.MaxTargets,
		MaxItemTargets:  config.MaxItemTargets,
		MaxGroupTargets: config.MaxGroupTargets,
	}
}
