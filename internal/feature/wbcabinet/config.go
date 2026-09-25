package wbcabinet

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	BootstrapOwnerID int64
}

func NewConfig() (Config, error) {
	var ownerID int64
	rawOwnerID := strings.TrimSpace(os.Getenv("ADMIN_TG_ID"))
	if rawOwnerID != "" {
		var err error
		ownerID, err = strconv.ParseInt(rawOwnerID, 10, 64)
		if err != nil || ownerID <= 0 {
			return Config{}, fmt.Errorf("ADMIN_TG_ID must be a positive integer")
		}
	}
	return Config{BootstrapOwnerID: ownerID}, nil
}

func NewConfigMust() Config {
	config, err := NewConfig()
	if err != nil {
		panic(fmt.Errorf("get wbcabinet config: %w", err))
	}
	return config
}
