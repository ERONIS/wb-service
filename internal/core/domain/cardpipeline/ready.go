package cardpipeline

import "time"

type ReadyBatchHeader struct {
	ID               BatchID
	Purpose          Purpose
	ItemsCount       int
	GroupsCount      int
	Checksum         Digest
	AuthorTelegramID int64
	FinalizedAt      time.Time
}

type ReadyBatchItem struct {
	ID             BatchItemID
	BatchID        BatchID
	Position       int
	SourceGroupKey SourceGroupKey
	VendorCode     string
	Payload        Card
}

type ReadyBatch struct {
	Header ReadyBatchHeader
	Items  []ReadyBatchItem
}

func (header ReadyBatchHeader) Validate() error {
	if header.ID <= 0 || !header.Purpose.IsValid() ||
		header.ItemsCount <= 0 || header.GroupsCount <= 0 ||
		header.GroupsCount > header.ItemsCount ||
		header.Checksum == (Digest{}) || header.AuthorTelegramID <= 0 ||
		header.FinalizedAt.IsZero() {
		return invalidBatch("ready batch header is invalid")
	}
	return nil
}

func (item ReadyBatchItem) Validate() error {
	if item.ID <= 0 || item.BatchID <= 0 || item.Position <= 0 ||
		item.SourceGroupKey == (SourceGroupKey{}) ||
		item.VendorCode == "" || item.Payload.Variant.VendorCode != item.VendorCode {
		return invalidBatch("ready batch item is invalid")
	}
	return nil
}
