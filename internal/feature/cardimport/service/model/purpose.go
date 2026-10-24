package model

type Purpose string

const PurposeTransfer Purpose = "transfer"

func (purpose Purpose) IsValid() bool {
	return purpose == PurposeTransfer
}
