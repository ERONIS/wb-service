package preparation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	core_observability "github.com/ERONIS/wb-service/internal/core/observability"
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
		logger:    core_observability.Logger(loggers...),
	}
}

// PrepareTarget owns the multi-read catalog workflow. The transport below it
// executes only individual operations and returns complete raw DTOs.
func (preparer *Preparer) PrepareTarget(
	ctx context.Context,
	cabinetID CabinetID,
	group SourceGroup,
) (prepared Result, err error) {
	startedAt := time.Now()
	logger := core_observability.LoggerWithContext(ctx, preparer.logger)
	core_observability.LogStarted(logger, "cardprepare", "catalog_prepare_target",
		zap.String("cabinet_id", string(cabinetID)), zap.Int("items_count", len(group.Items)))
	var (
		normalizeDuration       time.Duration
		limitsDuration          time.Duration
		subjectsDuration        time.Duration
		characteristicsDuration time.Duration
		brandsDuration          time.Duration
		directoriesDuration     time.Duration
		buildDuration           time.Duration
		subjectID               int64
	)
	defer func() {
		timingFields := []zap.Field{
			zap.String("cabinet_id", string(cabinetID)),
			zap.Int("items_count", len(group.Items)),
			zap.Int64("subject_id", subjectID),
			zap.String("outcome_code", string(prepared.Outcome.Code)),
			zap.Int("outcome_item_position", prepared.Outcome.ItemPosition),
			zap.String("outcome_field", prepared.Outcome.Field),
			zap.Duration("normalize_duration", normalizeDuration),
			zap.Duration("cards_limits_duration", limitsDuration),
			zap.Duration("subjects_duration", subjectsDuration),
			zap.Duration("characteristics_duration", characteristicsDuration),
			zap.Duration("brands_duration", brandsDuration),
			zap.Duration("directories_duration", directoriesDuration),
			zap.Duration("build_duration", buildDuration),
		}
		if cache, ok := preparer.transport.(*batchCatalog); ok {
			stats := cache.stats()
			timingFields = append(timingFields,
				zap.Int("catalog_cache_entries", stats.Entries),
				zap.Int("catalog_cache_bytes", stats.Bytes),
				zap.Int("catalog_cache_max_bytes", maxCatalogCacheBytes),
				zap.Uint64("catalog_cache_hits", stats.Hits),
				zap.Uint64("catalog_cache_misses", stats.Misses),
				zap.Uint64("catalog_cache_coalesced_waits", stats.CoalescedWaits),
				zap.Uint64("catalog_cache_stores", stats.Stores),
				zap.Uint64("catalog_cache_skipped_capacity", stats.SkippedCapacity),
				zap.Uint64("catalog_cache_evictions", stats.Evictions),
			)
		}
		if err == nil && prepared.Outcome.Code != "" && prepared.Outcome.Code != OutcomePrepared {
			fields := []zap.Field{
				zap.String("event", "card_preparation_rejected"),
				zap.String("cabinet_id", string(cabinetID)),
				zap.Int64("subject_id", subjectID),
				zap.String("outcome_code", string(prepared.Outcome.Code)),
				zap.Int("outcome_item_position", prepared.Outcome.ItemPosition),
				zap.String("outcome_field", prepared.Outcome.Field),
			}
			if prepared.Outcome.Details != nil {
				fields = append(fields, zap.Any("outcome_details", prepared.Outcome.Details))
			}
			logger.Warn("Card preparation rejected", fields...)
		}
		core_observability.LogTiming(
			logger,
			"cardprepare",
			"catalog_prepare_target",
			startedAt,
			err,
			timingFields...,
		)
	}()
	if ctx == nil {
		return Result{}, errors.New("prepare target: context is nil")
	}
	if strings.TrimSpace(string(cabinetID)) != string(cabinetID) ||
		cabinetID == "" {
		return Result{}, errors.New("prepare target: cabinet ID is invalid")
	}

	stepStartedAt := time.Now()
	normalizedGroup, err := normalizeSourceGroup(group)
	normalizeDuration = time.Since(stepStartedAt)
	if err != nil {
		return Result{}, err
	}
	if localResult := validateLocalGroupCategory(normalizedGroup); localResult != nil {
		return *localResult, nil
	}

	stepStartedAt = time.Now()
	limitsResponse, err := preparer.transport.CardsLimits(ctx, cabinetID)
	limitsDuration = time.Since(stepStartedAt)
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

	stepStartedAt = time.Now()
	subjects, subjectsResult, err := preparer.readSubjects(
		ctx,
		cabinetID,
		normalizedGroup.Items[0].Card.Category,
	)
	subjectsDuration = time.Since(stepStartedAt)
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

	stepStartedAt = time.Now()
	characteristicsResponse, err := preparer.transport.SubjectCharacteristics(
		ctx,
		cabinetID,
		subject.SubjectID,
		contentapi.SubjectCharacteristicsQuery{Locale: contentapi.LocaleRU},
	)
	characteristicsDuration = time.Since(stepStartedAt)
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
		stepStartedAt = time.Now()
		brands, brandsResult, err = preparer.readBrands(
			ctx,
			cabinetID,
			subject.SubjectID,
		)
		brandsDuration = time.Since(stepStartedAt)
		if err != nil {
			return Result{}, err
		}
		if brandsResult != nil {
			return *brandsResult, nil
		}
	}

	stepStartedAt = time.Now()
	directories, directoriesResult, err := preparer.readUsedDirectories(
		ctx,
		cabinetID,
		subject.SubjectID,
		normalizedGroup,
		characteristicsResponse.Data,
	)
	directoriesDuration = time.Since(stepStartedAt)
	if err != nil {
		return Result{}, err
	}
	if directoriesResult != nil {
		return *directoriesResult, nil
	}

	stepStartedAt = time.Now()
	prepared, err = Prepare(normalizedGroup, CatalogSnapshot{
		Subjects:        subjects,
		Characteristics: characteristicsResponse.Data,
		Brands:          brands,
		Directories:     directories,
		Limits:          limitsResponse.Data,
		ObservedAt:      limitsObservedAt,
	})
	buildDuration = time.Since(stepStartedAt)
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
	cabinetID CabinetID,
	category string,
) ([]contentapi.Subject, *Result, error) {
	result := make([]contentapi.Subject, 0)
	offset := 0
	for page := 0; page < maxCatalogPages; page++ {
		response, err := preparer.transport.Subjects(
			ctx,
			cabinetID,
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
	cabinetID CabinetID,
	subjectID int64,
) ([]contentapi.Brand, *Result, error) {
	result := make([]contentapi.Brand, 0)
	var next int64
	seen := make(map[int64]struct{})
	for page := 0; page < maxCatalogPages; page++ {
		response, err := preparer.transport.Brands(
			ctx,
			cabinetID,
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
	cabinetID CabinetID,
	subjectID int64,
	group SourceGroup,
	characteristics []contentapi.SubjectCharacteristic,
) (DirectorySnapshot, *Result, error) {
	used := usedDirectoryKinds(group, characteristics)
	result := DirectorySnapshot{}
	query := contentapi.DirectoryQuery{Locale: contentapi.LocaleRU}

	if used[directoryColors] {
		response, err := preparer.transport.Colors(ctx, cabinetID, query)
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
		response, err := preparer.transport.Kinds(ctx, cabinetID, query)
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
		response, err := preparer.transport.Countries(ctx, cabinetID, query)
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
		response, err := preparer.transport.Seasons(ctx, cabinetID, query)
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
		response, err := preparer.transport.VAT(ctx, cabinetID, query)
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
			cabinetID,
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
