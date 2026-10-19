package preparation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	core_logger "github.com/ERONIS/wb-service/internal/core/logger"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	"go.uber.org/zap"
)

const maxCatalogPages = 1000

type Preparer struct {
	transport CatalogTransport
	now       func() time.Time
	logger    *zap.Logger
}

func NewPreparer(transport CatalogTransport, loggers ...*zap.Logger) *Preparer {
	if transport == nil {
		panic("cardprepare catalog transport is nil")
	}
	return &Preparer{
		transport: transport,
		now:       time.Now,
		logger:    core_logger.Fallback(loggers...),
	}
}

func (preparer *Preparer) PrepareTarget(
	ctx context.Context,
	sellerID SellerID,
	group SourceGroup,
) (prepared Result, err error) {
	var subjectID int64
	defer func() {
		if preparer.logger == nil {
			return
		}
		if err == nil && prepared.Outcome.Code != "" && prepared.Outcome.Code != OutcomePrepared {
			fields := []zap.Field{
				zap.String("event", "card_preparation_rejected"),
				zap.String("seller_id", string(sellerID)),
				zap.Int64("subject_id", subjectID),
				zap.String("outcome_code", string(prepared.Outcome.Code)),
				zap.Int("outcome_item_position", prepared.Outcome.ItemPosition),
				zap.String("outcome_field", prepared.Outcome.Field),
				zap.Int("group_item_count", len(group.Items)),
			}
			for _, item := range group.Items {
				if item.Position != prepared.Outcome.ItemPosition {
					continue
				}
				fields = append(fields,
					zap.String("vendor_code", truncateDiagnosticValue(item.Card.Variant.VendorCode, 120)),
					zap.Ints("source_rows", item.Card.SourceRows),
					zap.String("category", truncateDiagnosticValue(item.Card.Category, 120)),
				)
				switch prepared.Outcome.Field {
				case "title":
					title := strings.TrimSpace(item.Card.Variant.Title)
					fields = append(fields,
						zap.Int("title_runes", utf8.RuneCountInString(title)),
						zap.Int("title_max_runes", contentapi.MaxProductTitleRunes),
						zap.String("title_input", truncateDiagnosticValue(title, 160)),
					)
				case "adult_only_unsupported":
					adult, valid := parseOptionalBoolean(item.Card.AdultOnly)
					fields = append(fields,
						zap.String("adult_only_input", truncateDiagnosticValue(item.Card.AdultOnly, 80)),
						zap.Bool("adult_only_parsed", adult),
						zap.Bool("adult_only_valid", valid),
					)
				}
				break
			}
			if prepared.Outcome.Details != nil {
				fields = append(fields, zap.Any("outcome_details", prepared.Outcome.Details))
			}
			preparer.logger.Warn("Card preparation rejected", fields...)
		}
	}()
	if ctx == nil {
		return Result{}, errors.New("prepare target: context is nil")
	}
	if strings.TrimSpace(string(sellerID)) != string(sellerID) ||
		sellerID == "" {
		return Result{}, errors.New("prepare target: seller ID is invalid")
	}

	normalizedGroup, err := normalizeSourceGroup(group)
	if err != nil {
		return Result{}, err
	}
	if localResult := validateLocalGroupCategory(normalizedGroup); localResult != nil {
		return *localResult, nil
	}

	limitsResponse, err := preparer.transport.CardsLimits(ctx, sellerID)
	if err != nil {
		return Result{}, fmt.Errorf("read WB Cards Limits: %w", err)
	}
	if limitsOutcome := envelopeOutcome(
		limitsResponse.ResponseMeta,
		"cards_limits",
	); limitsOutcome != nil {
		return *limitsOutcome, nil
	}
	limitsObservedAt := preparer.now().UTC().Truncate(time.Microsecond)
	if limitsObservedAt.IsZero() {
		return Result{}, errors.New("prepare target: current time is empty")
	}

	subjects, subjectsResult, err := preparer.readSubjects(
		ctx,
		sellerID,
		normalizedGroup.Items[0].Card.Category,
	)
	if err != nil {
		return Result{}, err
	}
	if subjectsResult != nil {
		return *subjectsResult, nil
	}
	subject, subjectResult := resolveSubject(normalizedGroup, subjects)
	if subjectResult != nil {
		return *subjectResult, nil
	}
	subjectID = subject.SubjectID

	characteristicsResponse, err := preparer.transport.SubjectCharacteristics(
		ctx,
		sellerID,
		subject.SubjectID,
		contentapi.SubjectCharacteristicsQuery{Locale: contentapi.LocaleRU},
	)
	if err != nil {
		return Result{}, fmt.Errorf("read WB subject characteristics: %w", err)
	}
	if characteristicsOutcome := envelopeOutcome(
		characteristicsResponse.ResponseMeta,
		"subject_characteristics",
	); characteristicsOutcome != nil {
		return *characteristicsOutcome, nil
	}

	brands := make([]contentapi.Brand, 0)
	if groupUsesBrand(normalizedGroup) {
		var brandsResult *Result
		brands, brandsResult, err = preparer.readBrands(
			ctx,
			sellerID,
			subject.SubjectID,
		)
		if err != nil {
			return Result{}, err
		}
		if brandsResult != nil {
			return *brandsResult, nil
		}
	}

	directories, directoriesResult, err := preparer.readUsedDirectories(
		ctx,
		sellerID,
		subject.SubjectID,
		normalizedGroup,
		characteristicsResponse.Data,
	)
	if err != nil {
		return Result{}, err
	}
	if directoriesResult != nil {
		return *directoriesResult, nil
	}

	prepared, err = Prepare(normalizedGroup, CatalogSnapshot{
		Subjects:        subjects,
		Characteristics: characteristicsResponse.Data,
		Brands:          brands,
		Directories:     directories,
		Limits:          limitsResponse.Data,
		ObservedAt:      limitsObservedAt,
	})
	return prepared, err
}

func validateLocalGroupCategory(group SourceGroup) *Result {
	categoryKey := normalizeKey(group.Items[0].Card.Category)
	if categoryKey == "" {
		result := outcome(OutcomeSubjectNotFound, 0, "category")
		return &result
	}
	for _, item := range group.Items[1:] {
		if normalizeKey(item.Card.Category) != categoryKey {
			result := outcome(
				OutcomeCharacteristicInvalid,
				item.Position,
				"category",
			)
			return &result
		}
	}
	return nil
}

func (preparer *Preparer) readSubjects(
	ctx context.Context,
	sellerID SellerID,
	category string,
) ([]contentapi.Subject, *Result, error) {
	result := make([]contentapi.Subject, 0)
	offset := 0
	for page := 0; page < maxCatalogPages; page++ {
		response, err := preparer.transport.Subjects(
			ctx,
			sellerID,
			contentapi.SubjectsQuery{
				Locale: contentapi.LocaleRU,
				Name:   category,
				Limit:  contentapi.MaxSubjectsPageSize,
				Offset: offset,
			},
		)
		if err != nil {
			return nil, nil, fmt.Errorf("read WB subjects: %w", err)
		}
		if responseOutcome := envelopeOutcome(
			response.ResponseMeta,
			"subjects",
		); responseOutcome != nil {
			return nil, responseOutcome, nil
		}
		result = append(result, response.Data...)
		if len(response.Data) < contentapi.MaxSubjectsPageSize {
			return result, nil, nil
		}
		offset += len(response.Data)
	}
	overflow := outcome(OutcomeCatalogContractError, 0, "subjects_pagination")
	return nil, &overflow, nil
}

func (preparer *Preparer) readBrands(
	ctx context.Context,
	sellerID SellerID,
	subjectID int64,
) ([]contentapi.Brand, *Result, error) {
	result := make([]contentapi.Brand, 0)
	var next int64
	seen := make(map[int64]struct{})
	for page := 0; page < maxCatalogPages; page++ {
		response, err := preparer.transport.Brands(
			ctx,
			sellerID,
			contentapi.BrandsQuery{SubjectID: subjectID, Next: next},
		)
		if err != nil {
			return nil, nil, fmt.Errorf("read WB brands: %w", err)
		}
		if response.Next < 0 || response.Total < 0 {
			invalid := outcome(
				OutcomeCatalogContractError, 0, "brands_pagination",
			)
			return nil, &invalid, nil
		}
		result = append(result, response.Brands...)
		if response.Next == 0 {
			return result, nil, nil
		}
		if _, exists := seen[response.Next]; exists {
			invalid := outcome(
				OutcomeCatalogContractError, 0, "brands_pagination",
			)
			return nil, &invalid, nil
		}
		seen[response.Next] = struct{}{}
		next = response.Next
	}
	overflow := outcome(OutcomeCatalogContractError, 0, "brands_pagination")
	return nil, &overflow, nil
}

type directoryKind uint8

const (
	directoryColors directoryKind = iota + 1
	directoryKinds
	directoryCountries
	directorySeasons
	directoryVAT
	directoryTNVED
)

func (preparer *Preparer) readUsedDirectories(
	ctx context.Context,
	sellerID SellerID,
	subjectID int64,
	group SourceGroup,
	characteristics []contentapi.SubjectCharacteristic,
) (DirectorySnapshot, *Result, error) {
	used := usedDirectoryKinds(group, characteristics)
	result := DirectorySnapshot{}
	query := contentapi.DirectoryQuery{Locale: contentapi.LocaleRU}

	if used[directoryColors] {
		response, err := preparer.transport.Colors(ctx, sellerID, query)
		if err != nil {
			return result, nil, fmt.Errorf("read WB colors directory: %w", err)
		}
		if responseOutcome := envelopeOutcome(
			response.ResponseMeta,
			"directory_colors",
		); responseOutcome != nil {
			return result, responseOutcome, nil
		}
		result.Colors = make([]string, 0, len(response.Data))
		for _, value := range response.Data {
			result.Colors = append(result.Colors, value.Name)
		}
	}
	if used[directoryKinds] {
		response, err := preparer.transport.Kinds(ctx, sellerID, query)
		if err != nil {
			return result, nil, fmt.Errorf("read WB kinds directory: %w", err)
		}
		if responseOutcome := envelopeOutcome(
			response.ResponseMeta,
			"directory_kinds",
		); responseOutcome != nil {
			return result, responseOutcome, nil
		}
		result.Kinds = append([]string(nil), response.Data...)
	}
	if used[directoryCountries] {
		response, err := preparer.transport.Countries(ctx, sellerID, query)
		if err != nil {
			return result, nil, fmt.Errorf("read WB countries directory: %w", err)
		}
		if responseOutcome := envelopeOutcome(
			response.ResponseMeta,
			"directory_countries",
		); responseOutcome != nil {
			return result, responseOutcome, nil
		}
		result.Countries = make([]string, 0, len(response.Data)*2)
		for _, value := range response.Data {
			result.Countries = append(result.Countries, value.Name)
			if strings.TrimSpace(value.FullName) != "" {
				result.Countries = append(result.Countries, value.FullName)
			}
		}
	}
	if used[directorySeasons] {
		response, err := preparer.transport.Seasons(ctx, sellerID, query)
		if err != nil {
			return result, nil, fmt.Errorf("read WB seasons directory: %w", err)
		}
		if responseOutcome := envelopeOutcome(
			response.ResponseMeta,
			"directory_seasons",
		); responseOutcome != nil {
			return result, responseOutcome, nil
		}
		result.Seasons = append([]string(nil), response.Data...)
	}
	if used[directoryVAT] {
		response, err := preparer.transport.VAT(ctx, sellerID, query)
		if err != nil {
			return result, nil, fmt.Errorf("read WB VAT directory: %w", err)
		}
		if responseOutcome := envelopeOutcome(
			response.ResponseMeta,
			"directory_vat",
		); responseOutcome != nil {
			return result, responseOutcome, nil
		}
		result.VAT = append([]string(nil), response.Data...)
	}
	if used[directoryTNVED] {
		response, err := preparer.transport.TNVED(
			ctx,
			sellerID,
			contentapi.DirectoryTNVEDQuery{
				SubjectID: subjectID,
				Locale:    contentapi.LocaleRU,
			},
		)
		if err != nil {
			return result, nil, fmt.Errorf("read WB TNVED directory: %w", err)
		}
		if responseOutcome := envelopeOutcome(
			response.ResponseMeta,
			"directory_tnved",
		); responseOutcome != nil {
			return result, responseOutcome, nil
		}
		result.TNVED = make([]string, 0, len(response.Data))
		for _, value := range response.Data {
			result.TNVED = append(result.TNVED, value.Code)
		}
	}
	return result, nil, nil
}

func usedDirectoryKinds(
	group SourceGroup,
	characteristics []contentapi.SubjectCharacteristic,
) map[directoryKind]bool {
	characteristicKindsByID := make(map[int64]directoryKind)
	characteristicKindsByName := make(map[string]directoryKind)
	for _, characteristic := range characteristics {
		name := normalizeKey(characteristic.Name)
		if kind, known := directoryKindForName(name); known {
			characteristicKindsByID[characteristic.CharacteristicID] = kind
			characteristicKindsByName[name] = kind
		}
	}

	used := make(map[directoryKind]bool)
	for _, item := range group.Items {
		for _, characteristic := range item.Card.Variant.Characteristics {
			if emptyCharacteristicValue(characteristic.Value) {
				continue
			}
			kind, known := characteristicKindsByID[characteristic.ID]
			if !known {
				kind, known = characteristicKindsByName[normalizeKey(characteristic.Name)]
			}
			if !known {
				kind, known = directoryKindForName(normalizeKey(characteristic.Name))
			}
			if known {
				used[kind] = true
			}
		}
		if strings.TrimSpace(item.Card.VATRate) != "" {
			used[directoryVAT] = true
		}
	}
	return used
}

func directoryKindForName(name string) (directoryKind, bool) {
	switch name {
	case "цвет", "цвет товара":
		return directoryColors, true
	case "пол":
		return directoryKinds, true
	case "страна производства":
		return directoryCountries, true
	case "сезон":
		return directorySeasons, true
	case vatCharacteristicName:
		return directoryVAT, true
	case "тн вэд", "тнвэд", "код тн вэд":
		return directoryTNVED, true
	default:
		return 0, false
	}
}

func groupUsesBrand(group SourceGroup) bool {
	for _, item := range group.Items {
		if strings.TrimSpace(item.Card.Variant.Brand) != "" {
			return true
		}
	}
	return false
}

func envelopeOutcome(
	metadata contentapi.ResponseMeta,
	field string,
) *Result {
	if !metadata.Error {
		return nil
	}
	result := outcome(OutcomeTargetTemporarilyUnavailable, 0, field)
	return &result
}
