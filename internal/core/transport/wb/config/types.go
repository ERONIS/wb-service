package config

import (
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
)

type Config struct {
	BaseURL  string
	Timeout  time.Duration
	Cabinets []CabinetConfig
}

type CabinetConfig struct {
	ID    CabinetID
	Name  string
	Token string `json:"-"`
}

type CabinetID = domain.CabinetID
