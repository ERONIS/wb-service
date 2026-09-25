package domain

import "crypto/sha256"

type CabinetID string

type SellerKey [sha256.Size]byte

type ClientGeneration [sha256.Size]byte
