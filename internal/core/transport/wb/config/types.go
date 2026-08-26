package config

import "time"

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

type CabinetID string
