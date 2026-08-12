package config

import (
	"fmt"
	"strings"
)

const redactedToken = "--- REDACTED ---"

func (config CabinetConfig) String() string {
	return config.format("CabinetConfig")
}

func (config CabinetConfig) GoString() string {
	return config.format("config.CabinetConfig")
}

func (config CabinetConfig) format(typeName string) string {
	token := config.Token
	if token != "" {
		token = redactedToken
	}

	return fmt.Sprintf(
		"%s{ID:%q, Name:%q, Token:%q}",
		typeName,
		config.ID,
		config.Name,
		token,
	)
}

func (config Config) String() string {
	return fmt.Sprintf(
		"Config{BaseURL:%q, Timeout:%q, Cabinets:%s}",
		config.BaseURL,
		config.Timeout.String(),
		formatCabinetConfigs(config.Cabinets),
	)
}

func (config Config) GoString() string {
	return config.String()
}

func formatCabinetConfigs(cabinets []CabinetConfig) string {
	var result strings.Builder

	result.WriteByte('[')

	for index, cabinet := range cabinets {
		if index > 0 {
			result.WriteString(", ")
		}

		result.WriteString(cabinet.String())
	}

	result.WriteByte(']')

	return result.String()
}
