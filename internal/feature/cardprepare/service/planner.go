package cardprepare_service

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
)

const vatCharacteristicName = "ставка ндс"

// Prepare is a deterministic, side-effect free card proposal builder. It has
// no gateway dependency and therefore cannot dispatch a WB mutation.
func Prepare(group SourceGroup, snapshot CatalogSnapshot) (Result, error) {
	group, err := normalizeSourceGroup(group)
	if err != nil {
		return Result{}, err
	}
	snapshot = normalizeCatalogSnapshot(snapshot)
	if snapshot.ObservedAt.IsZero() {
		return outcome(OutcomeCatalogContractError, 0, "observed_at"), nil
	}
	if snapshot.Limits.FreeLimits < 0 || snapshot.Limits.PaidLimits < 0 ||
		snapshot.Limits.FreeLimits > math.MaxInt64-snapshot.Limits.PaidLimits {
		return outcome(OutcomeCatalogContractError, 0, "cards_limits"), nil
	}

	subject, subjectResult := resolveSubject(group, snapshot.Subjects)
	if subjectResult != nil {
		return *subjectResult, nil
	}
	if int64(len(group.Items)) >
		snapshot.Limits.FreeLimits+snapshot.Limits.PaidLimits {
		return outcome(OutcomeCardLimitExceeded, 0, "cards_limits"), nil
	}

	schema, schemaResult := buildCharacteristicSchema(
		subject.SubjectID,
		snapshot.Characteristics,
	)
	if schemaResult != nil {
		return *schemaResult, nil
	}

	variants := make([]contentapi.UploadCard, 0, len(group.Items))
	for _, item := range group.Items {
		variant, itemResult := buildVariant(item, schema, snapshot)
		if itemResult != nil {
			return *itemResult, nil
		}
		variants = append(variants, variant)
	}

	request := contentapi.UploadCardsRequest{{
		SubjectID: subject.SubjectID,
		Variants:  variants,
	}}
	encoded, err := marshalAndValidateUploadCardsRequest(request)
	if err != nil {
		var validationError *uploadValidationError
		if errors.As(err, &validationError) {
			if validationError.Code == uploadValidationPayloadTooBig {
				return outcome(OutcomePayloadTooLarge, 0, "request"), nil
			}
			position := 0
			if validationError.VariantIndex >= 0 &&
				validationError.VariantIndex < len(group.Items) {
				position = group.Items[validationError.VariantIndex].Position
			}
			return outcome(
				OutcomeCharacteristicInvalid,
				position,
				validationError.Field,
			), nil
		}
		return Result{}, err
	}

	semanticDigest, err := digestJSON("cardprepare-semantic:v1", group)
	if err != nil {
		return Result{}, fmt.Errorf("digest source group: %w", err)
	}
	metadataDigest, err := digestJSON("cardprepare-metadata:v1", snapshot)
	if err != nil {
		return Result{}, fmt.Errorf("digest catalog snapshot: %w", err)
	}
	proposalRoot := digestProposal(semanticDigest, metadataDigest, encoded)

	return Result{
		Outcome: Outcome{Code: OutcomePrepared},
		Proposal: &Proposal{
			SubjectID:        subject.SubjectID,
			Request:          request,
			EncodedRequest:   append([]byte(nil), encoded...),
			MetadataSnapshot: snapshot,
			SemanticDigest:   semanticDigest,
			MetadataDigest:   metadataDigest,
			ProposalRoot:     proposalRoot,
			Limits:           snapshot.Limits,
			LimitsObservedAt: snapshot.ObservedAt,
		},
	}, nil
}

func resolveSubject(
	group SourceGroup,
	subjects []contentapi.Subject,
) (contentapi.Subject, *Result) {
	categoryKey := normalizeKey(group.Items[0].Card.Category)
	for _, item := range group.Items[1:] {
		if normalizeKey(item.Card.Category) != categoryKey {
			result := outcome(
				OutcomeCharacteristicInvalid,
				item.Position,
				"category",
			)
			return contentapi.Subject{}, &result
		}
	}

	matches := make([]contentapi.Subject, 0, 1)
	seen := make(map[int64]struct{})
	for _, subject := range subjects {
		if normalizeKey(subject.SubjectName) != categoryKey {
			continue
		}
		if subject.SubjectID <= 0 {
			result := outcome(
				OutcomeCatalogContractError, 0, "subject_id",
			)
			return contentapi.Subject{}, &result
		}
		if _, exists := seen[subject.SubjectID]; exists {
			continue
		}
		seen[subject.SubjectID] = struct{}{}
		matches = append(matches, subject)
	}

	switch len(matches) {
	case 0:
		result := outcome(OutcomeSubjectNotFound, 0, "category")
		return contentapi.Subject{}, &result
	case 1:
		return matches[0], nil
	default:
		result := outcome(OutcomeSubjectAmbiguous, 0, "category")
		return contentapi.Subject{}, &result
	}
}

type characteristicSchema struct {
	ordered []contentapi.SubjectCharacteristic
	byName  map[string]contentapi.SubjectCharacteristic
}

func buildCharacteristicSchema(
	subjectID int64,
	characteristics []contentapi.SubjectCharacteristic,
) (characteristicSchema, *Result) {
	schema := characteristicSchema{
		byName: make(map[string]contentapi.SubjectCharacteristic),
	}
	for _, characteristic := range characteristics {
		if characteristic.SubjectID != subjectID {
			continue
		}
		nameKey := normalizeKey(characteristic.Name)
		if characteristic.CharacteristicID <= 0 || nameKey == "" ||
			characteristic.MaxCount < 0 {
			result := outcome(
				OutcomeCatalogContractError, 0, "characteristics",
			)
			return characteristicSchema{}, &result
		}
		if existing, exists := schema.byName[nameKey]; exists {
			if existing.CharacteristicID == characteristic.CharacteristicID {
				continue
			}
			result := outcome(
				OutcomeCatalogContractError, 0, "characteristic_name",
			)
			return characteristicSchema{}, &result
		}
		schema.byName[nameKey] = characteristic
		schema.ordered = append(schema.ordered, characteristic)
	}
	sort.Slice(schema.ordered, func(left, right int) bool {
		return schema.ordered[left].CharacteristicID <
			schema.ordered[right].CharacteristicID
	})
	return schema, nil
}

func buildVariant(
	item SourceItem,
	schema characteristicSchema,
	snapshot CatalogSnapshot,
) (contentapi.UploadCard, *Result) {
	brand, brandResult := resolveBrand(item, snapshot.Brands)
	if brandResult != nil {
		return contentapi.UploadCard{}, brandResult
	}

	kizMarked, valid := parseOptionalBoolean(item.Card.MarkingConfirmed)
	if !valid {
		result := outcome(
			OutcomeCharacteristicInvalid,
			item.Position,
			"marking_confirmed",
		)
		return contentapi.UploadCard{}, &result
	}
	adult, valid := parseOptionalBoolean(item.Card.AdultOnly)
	if !valid || adult {
		result := outcome(
			OutcomeCharacteristicInvalid,
			item.Position,
			"adult_only_unsupported",
		)
		return contentapi.UploadCard{}, &result
	}

	values := make(map[string]string)
	for _, raw := range item.Card.Variant.Characteristics {
		nameKey := normalizeKey(raw.Name)
		if nameKey == "" || strings.TrimSpace(raw.Value) == "" {
			result := outcome(
				OutcomeCharacteristicInvalid,
				item.Position,
				"characteristics",
			)
			return contentapi.UploadCard{}, &result
		}
		if _, exists := values[nameKey]; exists {
			result := outcome(
				OutcomeCharacteristicInvalid,
				item.Position,
				"characteristic_duplicate",
			)
			return contentapi.UploadCard{}, &result
		}
		values[nameKey] = strings.TrimSpace(raw.Value)
	}
	if vat := strings.TrimSpace(item.Card.VATRate); vat != "" {
		if _, exists := values[vatCharacteristicName]; exists {
			result := outcome(
				OutcomeCharacteristicInvalid,
				item.Position,
				"vat_duplicate",
			)
			return contentapi.UploadCard{}, &result
		}
		values[vatCharacteristicName] = vat
	}

	for name := range values {
		if _, exists := schema.byName[name]; !exists {
			result := outcome(
				OutcomeCharacteristicInvalid,
				item.Position,
				"characteristic_unknown",
			)
			return contentapi.UploadCard{}, &result
		}
	}

	characteristics := make(
		[]contentapi.UploadCharacteristic,
		0,
		len(values),
	)
	for _, specification := range schema.ordered {
		nameKey := normalizeKey(specification.Name)
		rawValue, supplied := values[nameKey]
		if !supplied {
			if specification.Required {
				result := outcome(
					OutcomeCharacteristicMissing,
					item.Position,
					"characteristic_required",
				)
				return contentapi.UploadCard{}, &result
			}
			continue
		}

		value, valueResult := characteristicValue(
			item.Position,
			specification,
			rawValue,
			snapshot.Directories,
		)
		if valueResult != nil {
			return contentapi.UploadCard{}, valueResult
		}
		characteristics = append(
			characteristics,
			contentapi.UploadCharacteristic{
				ID:    specification.CharacteristicID,
				Value: value,
			},
		)
	}

	sizes := make([]contentapi.UploadSize, 0, len(item.Card.Variant.Sizes))
	for _, sourceSize := range item.Card.Variant.Sizes {
		price := item.Card.Price
		sizes = append(sizes, contentapi.UploadSize{
			TechSize: sourceSize.TechSize,
			WBSize:   sourceSize.WBSize,
			Price:    &price,
			SKUs:     append([]string(nil), sourceSize.SKUs...),
		})
	}

	return contentapi.UploadCard{
		VendorCode:  item.Card.Variant.VendorCode,
		KIZMarked:   kizMarked,
		Title:       item.Card.Variant.Title,
		Description: item.Card.Variant.Description,
		Brand:       brand,
		Dimensions: contentapi.UploadDimensions{
			Length:       item.Card.Variant.Dimensions.Length,
			Width:        item.Card.Variant.Dimensions.Width,
			Height:       item.Card.Variant.Dimensions.Height,
			WeightBrutto: item.Card.Variant.Dimensions.WeightBrutto,
		},
		Characteristics: characteristics,
		Sizes:           sizes,
	}, nil
}

func resolveBrand(
	item SourceItem,
	brands []contentapi.Brand,
) (string, *Result) {
	brandKey := normalizeKey(item.Card.Variant.Brand)
	if brandKey == "" {
		return "", nil
	}

	matches := make([]contentapi.Brand, 0, 1)
	seen := make(map[int64]struct{})
	for _, brand := range brands {
		if normalizeKey(brand.Name) != brandKey {
			continue
		}
		if brand.ID <= 0 {
			result := outcome(
				OutcomeCatalogContractError, item.Position, "brand_id",
			)
			return "", &result
		}
		if _, exists := seen[brand.ID]; exists {
			continue
		}
		seen[brand.ID] = struct{}{}
		matches = append(matches, brand)
	}

	switch len(matches) {
	case 0:
		result := outcome(OutcomeBrandNotFound, item.Position, "brand")
		return "", &result
	case 1:
		return matches[0].Name, nil
	default:
		result := outcome(
			OutcomeCatalogContractError, item.Position, "brand_ambiguous",
		)
		return "", &result
	}
}

func characteristicValue(
	position int,
	specification contentapi.SubjectCharacteristic,
	rawValue string,
	directories DirectorySnapshot,
) (any, *Result) {
	switch specification.CharacteristicType {
	case 0:
		result := outcome(
			OutcomeCharacteristicInvalid,
			position,
			"characteristic_deprecated",
		)
		return nil, &result
	case 1:
		values, valid := splitCharacteristicValues(rawValue)
		if !valid ||
			(specification.MaxCount > 0 &&
				len(values) > specification.MaxCount) {
			result := outcome(
				OutcomeCharacteristicInvalid,
				position,
				"characteristic_count",
			)
			return nil, &result
		}
		if directory, recognized := directoryFor(
			normalizeKey(specification.Name), directories,
		); recognized {
			if directory == nil {
				result := outcome(
					OutcomeCatalogContractError,
					position,
					"directory_not_loaded",
				)
				return nil, &result
			}
			for index, value := range values {
				canonical, found, ambiguous := exactDirectoryValue(
					value, directory,
				)
				if ambiguous {
					result := outcome(
						OutcomeCatalogContractError,
						position,
						"directory_ambiguous",
					)
					return nil, &result
				}
				if !found {
					result := outcome(
						OutcomeDirectoryValueInvalid,
						position,
						"directory_value",
					)
					return nil, &result
				}
				values[index] = canonical
			}
		}
		return values, nil
	case 4:
		value, err := strconv.ParseFloat(
			strings.ReplaceAll(strings.TrimSpace(rawValue), ",", "."),
			64,
		)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			result := outcome(
				OutcomeCharacteristicInvalid,
				position,
				"characteristic_number",
			)
			return nil, &result
		}
		return value, nil
	default:
		result := outcome(
			OutcomeCatalogContractError,
			position,
			"characteristic_type",
		)
		return nil, &result
	}
}

func directoryFor(
	characteristicName string,
	directories DirectorySnapshot,
) ([]string, bool) {
	switch characteristicName {
	case "цвет", "цвет товара":
		return directories.Colors, true
	case "пол":
		return directories.Kinds, true
	case "страна производства":
		return directories.Countries, true
	case "сезон":
		return directories.Seasons, true
	case vatCharacteristicName:
		return directories.VAT, true
	case "тн вэд", "тнвэд", "код тн вэд":
		return directories.TNVED, true
	default:
		return nil, false
	}
}

func exactDirectoryValue(
	value string,
	directory []string,
) (canonical string, found bool, ambiguous bool) {
	key := normalizeKey(value)
	for _, candidate := range directory {
		if normalizeKey(candidate) != key {
			continue
		}
		if !found {
			canonical = candidate
			found = true
			continue
		}
		if canonical != candidate {
			return "", false, true
		}
	}
	return canonical, found, false
}

func splitCharacteristicValues(value string) ([]string, bool) {
	parts := strings.Split(value, ";")
	values := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		key := normalizeKey(part)
		if key == "" {
			return nil, false
		}
		if _, exists := seen[key]; exists {
			return nil, false
		}
		seen[key] = struct{}{}
		values = append(values, part)
	}
	return values, len(values) > 0
}

func parseOptionalBoolean(value string) (bool, bool) {
	switch normalizeKey(value) {
	case "", "нет", "no", "false", "0", "-":
		return false, true
	case "да", "yes", "true", "1", "+":
		return true, true
	default:
		return false, false
	}
}

func normalizeSourceGroup(group SourceGroup) (SourceGroup, error) {
	if len(group.Items) == 0 {
		return SourceGroup{}, errors.New("cardprepare source group is empty")
	}

	items := make([]SourceItem, len(group.Items))
	for index, item := range group.Items {
		if item.Position <= 0 {
			return SourceGroup{}, errors.New(
				"cardprepare source item position must be positive",
			)
		}
		items[index] = SourceItem{
			Position: item.Position,
			Card:     cloneAndNormalizeCard(item.Card),
		}
	}
	sort.Slice(items, func(left, right int) bool {
		return items[left].Position < items[right].Position
	})
	for index := 1; index < len(items); index++ {
		if items[index-1].Position == items[index].Position {
			return SourceGroup{}, errors.New(
				"cardprepare source item position is duplicated",
			)
		}
	}
	return SourceGroup{Items: items}, nil
}

func cloneAndNormalizeCard(
	card cardimport_service.AggregatedCard,
) cardimport_service.AggregatedCard {
	card.SourceRows = append([]int(nil), card.SourceRows...)
	card.Group = strings.TrimSpace(card.Group)
	card.Category = strings.TrimSpace(card.Category)
	card.KIZRequirement = strings.TrimSpace(card.KIZRequirement)
	card.AdultOnly = strings.TrimSpace(card.AdultOnly)
	card.MarkingConfirmed = strings.TrimSpace(card.MarkingConfirmed)
	card.VATRate = strings.TrimSpace(card.VATRate)
	card.Variant.VendorCode = strings.TrimSpace(card.Variant.VendorCode)
	card.Variant.Title = strings.TrimSpace(card.Variant.Title)
	card.Variant.Description = strings.TrimSpace(card.Variant.Description)
	card.Variant.Brand = strings.TrimSpace(card.Variant.Brand)
	card.Variant.Sizes = append(
		[]cardimport_service.ParsedSize(nil),
		card.Variant.Sizes...,
	)
	for index := range card.Variant.Sizes {
		size := &card.Variant.Sizes[index]
		size.TechSize = strings.TrimSpace(size.TechSize)
		size.WBSize = strings.TrimSpace(size.WBSize)
		size.SKUs = append([]string(nil), size.SKUs...)
		for skuIndex := range size.SKUs {
			size.SKUs[skuIndex] = strings.TrimSpace(size.SKUs[skuIndex])
		}
		sort.Strings(size.SKUs)
	}
	sort.Slice(card.Variant.Sizes, func(left, right int) bool {
		leftSize := card.Variant.Sizes[left]
		rightSize := card.Variant.Sizes[right]
		leftKey := leftSize.TechSize + "\x00" + leftSize.WBSize + "\x00" +
			strings.Join(leftSize.SKUs, "\x00")
		rightKey := rightSize.TechSize + "\x00" + rightSize.WBSize + "\x00" +
			strings.Join(rightSize.SKUs, "\x00")
		return leftKey < rightKey
	})
	card.Variant.Characteristics = append(
		[]cardimport_service.RawCharacteristic(nil),
		card.Variant.Characteristics...,
	)
	for index := range card.Variant.Characteristics {
		characteristic := &card.Variant.Characteristics[index]
		characteristic.Name = strings.TrimSpace(characteristic.Name)
		characteristic.Value = strings.TrimSpace(characteristic.Value)
	}
	sort.Slice(card.Variant.Characteristics, func(left, right int) bool {
		leftCharacteristic := card.Variant.Characteristics[left]
		rightCharacteristic := card.Variant.Characteristics[right]
		leftKey := normalizeKey(leftCharacteristic.Name) + "\x00" +
			leftCharacteristic.Value
		rightKey := normalizeKey(rightCharacteristic.Name) + "\x00" +
			rightCharacteristic.Value
		return leftKey < rightKey
	})
	card.Media.Photos = append([]string(nil), card.Media.Photos...)
	return card
}

func normalizeCatalogSnapshot(snapshot CatalogSnapshot) CatalogSnapshot {
	snapshot.ObservedAt = snapshot.ObservedAt.UTC()
	snapshot.Subjects = append([]contentapi.Subject(nil), snapshot.Subjects...)
	sort.Slice(snapshot.Subjects, func(left, right int) bool {
		if snapshot.Subjects[left].SubjectID != snapshot.Subjects[right].SubjectID {
			return snapshot.Subjects[left].SubjectID <
				snapshot.Subjects[right].SubjectID
		}
		return snapshot.Subjects[left].SubjectName <
			snapshot.Subjects[right].SubjectName
	})
	snapshot.Characteristics = append(
		[]contentapi.SubjectCharacteristic(nil),
		snapshot.Characteristics...,
	)
	sort.Slice(snapshot.Characteristics, func(left, right int) bool {
		if snapshot.Characteristics[left].SubjectID !=
			snapshot.Characteristics[right].SubjectID {
			return snapshot.Characteristics[left].SubjectID <
				snapshot.Characteristics[right].SubjectID
		}
		if snapshot.Characteristics[left].CharacteristicID !=
			snapshot.Characteristics[right].CharacteristicID {
			return snapshot.Characteristics[left].CharacteristicID <
				snapshot.Characteristics[right].CharacteristicID
		}
		return snapshot.Characteristics[left].Name <
			snapshot.Characteristics[right].Name
	})
	snapshot.Brands = append([]contentapi.Brand(nil), snapshot.Brands...)
	sort.Slice(snapshot.Brands, func(left, right int) bool {
		if snapshot.Brands[left].ID != snapshot.Brands[right].ID {
			return snapshot.Brands[left].ID < snapshot.Brands[right].ID
		}
		return snapshot.Brands[left].Name < snapshot.Brands[right].Name
	})
	snapshot.Directories = normalizeDirectories(snapshot.Directories)
	return snapshot
}

func normalizeDirectories(directories DirectorySnapshot) DirectorySnapshot {
	directories.Colors = cloneAndSortStrings(directories.Colors)
	directories.Kinds = cloneAndSortStrings(directories.Kinds)
	directories.Countries = cloneAndSortStrings(directories.Countries)
	directories.Seasons = cloneAndSortStrings(directories.Seasons)
	directories.VAT = cloneAndSortStrings(directories.VAT)
	directories.TNVED = cloneAndSortStrings(directories.TNVED)
	return directories
}

func cloneAndSortStrings(values []string) []string {
	if values == nil {
		return nil
	}
	result := append([]string(nil), values...)
	for index := range result {
		result[index] = strings.TrimSpace(result[index])
	}
	sort.Strings(result)
	return result
}

func normalizeKey(value string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(strings.TrimSpace(value)), func(
		character rune,
	) bool {
		return unicode.IsSpace(character)
	}), " ")
}

func digestJSON(domain string, value any) (Digest, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return Digest{}, err
	}
	hasher := sha256.New()
	_, _ = hasher.Write([]byte(domain))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write(encoded)
	var digest Digest
	copy(digest[:], hasher.Sum(nil))
	return digest, nil
}

func digestProposal(
	semanticDigest Digest,
	metadataDigest Digest,
	request []byte,
) Digest {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("cardprepare-proposal:v1"))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write(semanticDigest[:])
	_, _ = hasher.Write(metadataDigest[:])
	_, _ = hasher.Write(request)
	var digest Digest
	copy(digest[:], hasher.Sum(nil))
	return digest
}

func outcome(code OutcomeCode, position int, field string) Result {
	return Result{Outcome: Outcome{
		Code:         code,
		ItemPosition: position,
		Field:        field,
	}}
}
