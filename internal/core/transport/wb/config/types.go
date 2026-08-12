package config

import "time"

type CabinetID string

type CabinetConfig struct {
	ID    CabinetID
	Name  string
	Token string `json:"-"`
}

type Config struct {
	BaseURL  string
	Timeout  time.Duration
	Cabinets []CabinetConfig
}
