package config

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	maxCabinetIDLength    = 128
	maxCabinetNameLength  = 128
	maxCabinetTokenLength = 16 * 1024
)

func (config Config) Validate() error {
	if config.BaseURL == "" {
		return fmt.Errorf("base URL is empty")
	}
	if strings.TrimSpace(config.BaseURL) != config.BaseURL {
		return fmt.Errorf("base URL contains surrounding whitespace")
	}
	if config.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}
	seenIDs := make(map[CabinetID]struct{}, len(config.Cabinets))
	seenNames := make(map[string]CabinetID, len(config.Cabinets))

	for index, cabinet := range config.Cabinets {
		if cabinet.ID == "" {
			return fmt.Errorf(
				"cabinet ID at position %d is empty",
				index,
			)
		}

		if _, exists := seenIDs[cabinet.ID]; exists {
			return fmt.Errorf(
				"cabinet ID %q is duplicated",
				cabinet.ID,
			)
		}
		seenIDs[cabinet.ID] = struct{}{}

		if err := cabinet.Validate(); err != nil {
			return err
		}

		normalizedName := strings.ToLower(
			strings.TrimSpace(cabinet.Name),
		)

		if existingID, exists := seenNames[normalizedName]; exists {
			return fmt.Errorf(
				"cabinets %q and %q have duplicate names",
				existingID,
				cabinet.ID,
			)
		}
		seenNames[normalizedName] = cabinet.ID
	}

	return nil
}

func (config CabinetConfig) Validate() error {
	if config.ID == "" || strings.TrimSpace(string(config.ID)) != string(config.ID) ||
		len(config.ID) > maxCabinetIDLength {
		return fmt.Errorf("cabinet ID %q is invalid", config.ID)
	}
	if err := validateCabinetName(config.Name); err != nil {
		return fmt.Errorf(
			"validate cabinet %q name: %w",
			config.ID,
			err,
		)
	}

	if err := validateCabinetToken(config.Token); err != nil {
		return fmt.Errorf(
			"validate cabinet %q token: %w",
			config.ID,
			err,
		)
	}

	return nil
}

func validateCabinetName(name string) error {
	normalizedName := strings.TrimSpace(name)

	if normalizedName == "" {
		return fmt.Errorf("cabinet name is empty")
	}
	if !utf8.ValidString(normalizedName) {
		return fmt.Errorf("cabinet name is not valid UTF-8")
	}
	if utf8.RuneCountInString(normalizedName) >
		maxCabinetNameLength {
		return fmt.Errorf(
			"cabinet name exceeds %d characters",
			maxCabinetNameLength,
		)
	}

	return nil
}

func validateCabinetToken(token string) error {
	if token == "" {
		return fmt.Errorf("cabinet token is empty")
	}
	if len(token) > maxCabinetTokenLength {
		return fmt.Errorf(
			"cabinet token exceeds %d bytes",
			maxCabinetTokenLength,
		)
	}
	if strings.TrimSpace(token) != token {
		return fmt.Errorf(
			"cabinet token contains surrounding whitespace",
		)
	}

	return nil
}
