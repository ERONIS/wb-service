package cardpublication_config_transport

import (
	"fmt"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
)

type MediaUploadMethod string

const (
	MediaUploadByLinks MediaUploadMethod = "links"
	MediaUploadByFile  MediaUploadMethod = "file"
)

type Config struct {
	ReconciliationDelay   time.Duration     `envconfig:"RECONCILIATION_DELAY" default:"10s"`
	ReconciliationTimeout time.Duration     `envconfig:"RECONCILIATION_TIMEOUT" default:"30m"`
	MediaAutoDispatch     bool              `envconfig:"MEDIA_AUTO_DISPATCH" default:"true"`
	MediaDispatchInterval time.Duration     `envconfig:"MEDIA_DISPATCH_INTERVAL" default:"3s"`
	MediaCheckInterval    time.Duration     `envconfig:"MEDIA_CHECK_INTERVAL" default:"1m"`
	MediaCheckTimeout     time.Duration     `envconfig:"MEDIA_CHECK_TIMEOUT" default:"30m"`
	ProductConcurrency    int               `envconfig:"PRODUCT_CONCURRENCY" default:"5"`
	MediaConcurrency      int               `envconfig:"MEDIA_CONCURRENCY" default:"20"`
	MediaUploadMethod     MediaUploadMethod `envconfig:"MEDIA_UPLOAD_METHOD" default:"links"`
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
	if config.MediaUploadMethod != MediaUploadByLinks &&
		config.MediaUploadMethod != MediaUploadByFile {
		return fmt.Errorf("media upload method must be %q or %q", MediaUploadByLinks, MediaUploadByFile)
	}
	if config.ReconciliationDelay <= 0 {
		return fmt.Errorf("reconciliation delay must be positive")
	}
	if config.ReconciliationTimeout <= config.ReconciliationDelay ||
		config.ReconciliationTimeout > 24*time.Hour {
		return fmt.Errorf("reconciliation timeout must be greater than delay and at most 24 hours")
	}
	if config.MediaCheckInterval <= 0 {
		return fmt.Errorf("media check interval must be positive")
	}
	if config.MediaDispatchInterval <= 0 {
		return fmt.Errorf("media dispatch interval must be positive")
	}
	if config.MediaCheckTimeout <= config.EffectiveMediaCheckInterval() ||
		config.MediaCheckTimeout > 24*time.Hour {
		return fmt.Errorf("media check timeout must be greater than interval and at most 24 hours")
	}
	if config.ProductConcurrency <= 0 || config.ProductConcurrency > 20 {
		return fmt.Errorf("product concurrency must be between 1 and 20")
	}
	if config.MediaConcurrency <= 0 || config.MediaConcurrency > 20 {
		return fmt.Errorf("media concurrency must be between 1 and 20")
	}
	return nil
}

func (method *MediaUploadMethod) Decode(value string) error {
	*method = MediaUploadMethod(strings.ToLower(strings.TrimSpace(value)))
	return nil
}

func (config Config) EffectiveMediaCheckInterval() time.Duration {
	return max(config.MediaCheckInterval, time.Minute)
}
