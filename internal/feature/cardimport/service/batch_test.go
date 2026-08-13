package cardimport_service

import (
	"testing"
)

func TestSourceGroupKeyScopesNamedGroupsToSourceFile(t *testing.T) {
	t.Parallel()

	first := NewSourceGroupKey(10, "group-a", "vendor-a")
	sameGroup := NewSourceGroupKey(10, "group-a", "vendor-b")
	otherFile := NewSourceGroupKey(11, "group-a", "vendor-a")

	if first != sameGroup {
		t.Fatal("named group key depends on vendor code")
	}
	if first == otherFile {
		t.Fatal("named group key does not depend on source file")
	}
}

func TestSourceGroupKeyUsesVendorForEmptyGroup(t *testing.T) {
	t.Parallel()

	first := NewSourceGroupKey(10, "", "vendor-a")
	same := NewSourceGroupKey(10, "", "vendor-a")
	otherVendor := NewSourceGroupKey(10, "", "vendor-b")

	if first != same {
		t.Fatal("singleton group key is not deterministic")
	}
	if first == otherVendor {
		t.Fatal("singleton group key does not depend on vendor code")
	}
}

func TestBatchChecksumIsStableAndExcludesStorageIDs(t *testing.T) {
	t.Parallel()

	items := testBatchItems()
	first, err := BatchChecksum(PurposeTransfer, 2, items)
	if err != nil {
		t.Fatalf("BatchChecksum() error = %v", err)
	}

	items[0].ID = 99
	items[0].BatchID = 88
	second, err := BatchChecksum(PurposeTransfer, 2, items)
	if err != nil {
		t.Fatalf("BatchChecksum() replay error = %v", err)
	}
	if first != second {
		t.Fatal("checksum depends on batch or batch item storage ID")
	}

	items[0].Payload.Variant.Title = "changed"
	changed, err := BatchChecksum(PurposeTransfer, 2, items)
	if err != nil {
		t.Fatalf("BatchChecksum() changed error = %v", err)
	}
	if first == changed {
		t.Fatal("checksum does not detect typed payload change")
	}
}

func TestBatchChecksumDetectsOrderedSourceChange(t *testing.T) {
	t.Parallel()

	items := testBatchItems()
	first, err := BatchChecksum(PurposeTransfer, 2, items)
	if err != nil {
		t.Fatalf("BatchChecksum() error = %v", err)
	}

	items[0], items[1] = items[1], items[0]
	items[0].Position = 1
	items[1].Position = 2
	changed, err := BatchChecksum(PurposeTransfer, 2, items)
	if err != nil {
		t.Fatalf("BatchChecksum() reordered error = %v", err)
	}
	if first == changed {
		t.Fatal("checksum does not detect source order change")
	}
}

func testBatchItems() []BatchItem {
	first := BatchItem{
		Position:     1,
		SourceFileID: 10,
		SourceRows:   []int{2, 3},
		VendorCode:   "vendor-a",
		Payload: AggregatedCard{
			SourceRows: []int{2, 3},
			Group:      "group-a",
			Variant: ParsedVariant{
				VendorCode: "vendor-a",
				Title:      "first",
			},
		},
	}
	first.SourceGroupKey = NewSourceGroupKey(
		first.SourceFileID,
		first.Payload.Group,
		first.VendorCode,
	)

	second := BatchItem{
		Position:     2,
		SourceFileID: 11,
		SourceRows:   []int{5},
		VendorCode:   "vendor-b",
		Payload: AggregatedCard{
			SourceRows: []int{5},
			Variant: ParsedVariant{
				VendorCode: "vendor-b",
				Title:      "second",
			},
		},
	}
	second.SourceGroupKey = NewSourceGroupKey(
		second.SourceFileID,
		second.Payload.Group,
		second.VendorCode,
	)

	return []BatchItem{first, second}
}
