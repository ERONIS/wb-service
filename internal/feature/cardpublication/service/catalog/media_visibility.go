package catalog

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

type MediaVisibility struct {
	Visible      bool
	CardFound    bool
	PhotosCount  int
	VideoVisible bool
}

// CheckMediaVisibility makes one fresh observation. Previously cached photos
// must not decide whether an asynchronous WB media request has completed.
func (reader *CatalogReader) CheckMediaVisibility(ctx context.Context, cabinetID CabinetID,
	vendorCode string, nmID int64, expectedPhotos int, expectedVideo bool,
) (MediaVisibility, error) {
	vendorCode = normalizeCatalogVendorCode(vendorCode)
	if ctx == nil || cabinetID == "" || vendorCode == "" || nmID <= 0 || expectedPhotos < 0 {
		return MediaVisibility{}, errors.New("WB media visibility query is invalid")
	}
	cards, err := reader.readNormalVendorCode(ctx, cabinetID, vendorCode)
	if err != nil {
		return MediaVisibility{}, err
	}
	for _, card := range cards {
		reader.recent.StoreIfRecent(cabinetID, card)
		if card.NMID == nmID {
			result := MediaVisibility{CardFound: true, PhotosCount: loadedPhotoCount(card.Photos),
				VideoVisible: strings.TrimSpace(card.Video) != ""}
			result.Visible = result.PhotosCount >= expectedPhotos && (!expectedVideo || result.VideoVisible)
			return result, nil
		}
	}
	if nmID > 0 {
		nmCards, err := reader.readNormalVendorCode(ctx, cabinetID, strconv.FormatInt(nmID, 10))
		if err == nil {
			for _, card := range nmCards {
				reader.recent.StoreIfRecent(cabinetID, card)
				if card.NMID == nmID {
					result := MediaVisibility{CardFound: true, PhotosCount: loadedPhotoCount(card.Photos),
						VideoVisible: strings.TrimSpace(card.Video) != ""}
					result.Visible = result.PhotosCount >= expectedPhotos && (!expectedVideo || result.VideoVisible)
					return result, nil
				}
			}
		}
	}
	return MediaVisibility{}, nil
}
