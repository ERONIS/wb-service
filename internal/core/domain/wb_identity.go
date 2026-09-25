// Package domain contains credential-safe value objects shared by WB
// domain features and the WB transport. None of these values exposes a token.
package domain

import "crypto/sha256"

type CabinetID string

type SellerKey [sha256.Size]byte

type ClientGeneration [sha256.Size]byte
