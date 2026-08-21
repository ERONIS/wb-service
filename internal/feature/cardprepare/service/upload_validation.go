package cardprepare_service

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
)

type uploadValidationCode string

const (
	uploadValidationInvalid       uploadValidationCode = "invalid"
	uploadValidationPayloadTooBig uploadValidationCode = "payload_too_large"
)

type uploadValidationError struct {
	Code         uploadValidationCode
	GroupIndex   int
	VariantIndex int
	Field        string
}

func (validationError *uploadValidationError) Error() string {
	if validationError == nil {
		return "upload request validation failed"
	}
	return fmt.Sprintf(
		"upload request validation failed: code=%s group=%d variant=%d field=%s",
		validationError.Code,
		validationError.GroupIndex,
		validationError.VariantIndex,
		validationError.Field,
	)
}

func marshalAndValidateUploadCardsRequest(
	request contentapi.UploadCardsRequest,
) ([]byte, error) {
	if len(request) == 0 || len(request) > contentapi.MaxUploadGroups {
		return nil, invalidUploadAt(-1, -1, "groups")
	}

	vendorCodes := make(map[string]struct{})
	for groupIndex, group := range request {
		if group.SubjectID <= 0 {
			return nil, invalidUploadAt(groupIndex, -1, "subjectID")
		}
		if len(group.Variants) == 0 ||
			len(group.Variants) > contentapi.MaxVariantsPerGroup {
			return nil, invalidUploadAt(groupIndex, -1, "variants")
		}

		for variantIndex, variant := range group.Variants {
			if strings.TrimSpace(variant.VendorCode) == "" {
				return nil, invalidUploadAt(
					groupIndex, variantIndex, "vendorCode",
				)
			}
			if _, exists := vendorCodes[variant.VendorCode]; exists {
				return nil, invalidUploadAt(
					groupIndex, variantIndex, "vendorCode",
				)
			}
			vendorCodes[variant.VendorCode] = struct{}{}

			if utf8.RuneCountInString(variant.Title) >
				contentapi.MaxProductTitleRunes {
				return nil, invalidUploadAt(groupIndex, variantIndex, "title")
			}
			if utf8.RuneCountInString(variant.Description) >
				contentapi.MaxProductDescriptionRunes {
				return nil, invalidUploadAt(
					groupIndex, variantIndex, "description",
				)
			}
			if !validDimensions(variant.Dimensions) {
				return nil, invalidUploadAt(
					groupIndex, variantIndex, "dimensions",
				)
			}
			if len(variant.Sizes) == 0 {
				return nil, invalidUploadAt(groupIndex, variantIndex, "sizes")
			}

			characteristicIDs := make(map[int64]struct{})
			for _, characteristic := range variant.Characteristics {
				if characteristic.ID <= 0 || characteristic.Value == nil {
					return nil, invalidUploadAt(
						groupIndex, variantIndex, "characteristics",
					)
				}
				if _, exists := characteristicIDs[characteristic.ID]; exists {
					return nil, invalidUploadAt(
						groupIndex, variantIndex, "characteristics",
					)
				}
				characteristicIDs[characteristic.ID] = struct{}{}
			}

			for _, size := range variant.Sizes {
				if size.Price != nil && *size.Price <= 0 {
					return nil, invalidUploadAt(
						groupIndex, variantIndex, "sizes.price",
					)
				}
				for _, sku := range size.SKUs {
					if strings.TrimSpace(sku) == "" {
						return nil, invalidUploadAt(
							groupIndex, variantIndex, "sizes.skus",
						)
					}
				}
			}
		}
	}

	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal upload cards request: %w", err)
	}
	if int64(len(encoded)) >
		contentapi.UploadCardsOperation().MaxRequestBytes() {
		return nil, &uploadValidationError{
			Code:         uploadValidationPayloadTooBig,
			GroupIndex:   -1,
			VariantIndex: -1,
			Field:        "request",
		}
	}
	return encoded, nil
}

func invalidUploadAt(groupIndex, variantIndex int, field string) error {
	return &uploadValidationError{
		Code:         uploadValidationInvalid,
		GroupIndex:   groupIndex,
		VariantIndex: variantIndex,
		Field:        field,
	}
}

func validDimensions(dimensions contentapi.UploadDimensions) bool {
	return validPositiveNumber(dimensions.Length) &&
		validPositiveNumber(dimensions.Width) &&
		validPositiveNumber(dimensions.Height) &&
		validPositiveNumber(dimensions.WeightBrutto)
}

func validPositiveNumber(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
