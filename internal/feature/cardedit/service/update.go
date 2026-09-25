package cardedit_service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"

	"go.uber.org/zap"
)

func (service *Service) processTarget(job Job, target Target) TargetResult {
	result := TargetResult{CabinetName: target.Cabinet.Name, NMID: target.NMID}
	cabinet, err := service.reauthorize(job.OwnerTelegramID, target.Cabinet)
	if err != nil {
		result.Err = err
		return result
	}
	current, err := service.findCardByNMID(service.ctx, target)
	if err != nil {
		result.Err = fmt.Errorf("получить актуальную карточку: %w", err)
		return result
	}
	if !strings.EqualFold(strings.TrimSpace(current.VendorCode), job.VendorCode) {
		result.Err = fmt.Errorf("артикул карточки изменился: %w", ErrUnsafeUpdate)
		return result
	}

	request, media, err := service.buildUpdate(service.ctx, target, current, job.Card)
	if err != nil {
		result.Err = err
		return result
	}
	startedAt := time.Now().UTC()
	response, err := service.gateway.UpdateCards(
		service.ctx,
		cabinet.ID,
		cabinet.ClientGeneration,
		contentapi.UpdateCardsRequest{request},
	)
	if err != nil {
		result.Err = fmt.Errorf("ошибка отправки запроса редактирования в WB: %w", err)
		return result
	}
	if response.Error {
		result.Err = responseError("редактирование", response.ResponseMeta)
		return result
	}
	if len(media) > 0 {
		mediaResponse, mediaErr := service.gateway.SaveMediaByLinks(
			service.ctx,
			cabinet.ID,
			cabinet.ClientGeneration,
			contentapi.SaveMediaByLinksRequest{NMID: target.NMID, Data: media},
		)
		if mediaErr != nil {
			result.Err = fmt.Errorf("ошибка отправки медиафайлов в WB: %w", mediaErr)
			return result
		}
		if mediaResponse.Error {
			result.Err = responseError("сохранение медиа", mediaResponse.ResponseMeta)
			return result
		}
	}

	for attempt := 0; attempt < service.config.MaxAttempts; attempt++ {
		if err := waitContext(service.ctx, service.config.PollInterval); err != nil {
			result.Err = err
			return result
		}
		wbErrors, checkErr := service.recentWBErrors(
			service.ctx,
			cabinet.ID,
			job.VendorCode,
			startedAt,
		)
		if checkErr != nil {
			service.logger.Debug(
				"check card edit WB errors",
				zap.Error(checkErr),
				zap.String("cabinet_id", string(cabinet.ID)),
				zap.Int64("nm_id", target.NMID),
			)
		} else if len(wbErrors) > 0 {
			result.WBErrors = wbErrors
			return result
		}

		observed, observeErr := service.findCardByNMID(service.ctx, target)
		if observeErr != nil {
			service.logger.Debug(
				"check edited card content",
				zap.Error(observeErr),
				zap.String("cabinet_id", string(cabinet.ID)),
				zap.Int64("nm_id", target.NMID),
			)
			continue
		}
		if updateConfirmed(observed, request, job.Card.Media, len(media) > 0) {
			result.Confirmed = true
			return result
		}
	}

	result.Unconfirmed = true
	return result
}

func (service *Service) reauthorize(
	ownerTelegramID int64,
	snapshot Cabinet,
) (Cabinet, error) {
	cabinets, err := service.gateway.Cabinets(service.ctx, ownerTelegramID)
	if err != nil {
		return Cabinet{}, fmt.Errorf("повторно проверить доступ к кабинету: %w", err)
	}
	for _, current := range cabinets {
		if current.ID != snapshot.ID {
			continue
		}
		if current.BindingRevision != snapshot.BindingRevision ||
			current.CapabilityRevision != snapshot.CapabilityRevision ||
			current.ClientGeneration != snapshot.ClientGeneration {
			return Cabinet{}, fmt.Errorf("доступ или токен кабинета изменился: %w", ErrUnsafeUpdate)
		}
		return current, nil
	}
	return Cabinet{}, fmt.Errorf("кабинет больше не доступен пользователю: %w", ErrUnsafeUpdate)
}

func (service *Service) buildUpdate(
	ctx context.Context,
	target Target,
	current contentapi.Card,
	edited cardpipeline.Card,
) (contentapi.UpdateCard, []string, error) {
	if len(current.Sizes) == 0 {
		return contentapi.UpdateCard{}, nil, fmt.Errorf("WB не вернул размеры и баркоды: %w", ErrUnsafeUpdate)
	}
	characteristics, err := service.mergeCharacteristics(ctx, target, current, edited)
	if err != nil {
		return contentapi.UpdateCard{}, nil, err
	}
	kizMarked := current.KIZMarked
	if strings.TrimSpace(edited.MarkingConfirmed) != "" {
		parsed, valid := parseOptionalBoolean(edited.MarkingConfirmed)
		if !valid {
			return contentapi.UpdateCard{}, nil, fmt.Errorf("некорректное значение подтверждения маркировки %q", edited.MarkingConfirmed)
		}
		kizMarked = parsed
	}
	sizes := make([]contentapi.UpdateSize, len(current.Sizes))
	for index, size := range current.Sizes {
		if size.CHRTID <= 0 || len(size.SKUs) == 0 {
			return contentapi.UpdateCard{}, nil, fmt.Errorf("размер карточки не содержит chrtID или баркод: %w", ErrUnsafeUpdate)
		}
		sizes[index] = contentapi.UpdateSize{
			CHRTID:   size.CHRTID,
			TechSize: size.TechSize,
			WBSize:   size.WBSize,
			SKUs:     append([]string(nil), size.SKUs...),
		}
	}
	media := append([]string(nil), edited.Media.Photos...)
	if video := strings.TrimSpace(edited.Media.Video); video != "" {
		media = append(media, video)
	}
	if len(media) > contentapi.MaxMediaLinks || len(edited.Media.Photos) > contentapi.MaxMediaImages {
		return contentapi.UpdateCard{}, nil, fmt.Errorf("слишком много медиафайлов")
	}
	return contentapi.UpdateCard{
		NMID:        current.NMID,
		VendorCode:  current.VendorCode,
		KIZMarked:   kizMarked,
		Brand:       strings.TrimSpace(edited.Variant.Brand),
		Title:       strings.TrimSpace(edited.Variant.Title),
		Description: strings.TrimSpace(edited.Variant.Description),
		Dimensions: contentapi.UploadDimensions{
			Length:       edited.Variant.Dimensions.Length,
			Width:        edited.Variant.Dimensions.Width,
			Height:       edited.Variant.Dimensions.Height,
			WeightBrutto: edited.Variant.Dimensions.WeightBrutto,
		},
		Characteristics: characteristics,
		Sizes:           sizes,
	}, media, nil
}

func (service *Service) mergeCharacteristics(
	ctx context.Context,
	target Target,
	current contentapi.Card,
	edited cardpipeline.Card,
) ([]contentapi.UploadCharacteristic, error) {
	schemaResponse, err := service.gateway.SubjectCharacteristics(
		ctx,
		target.Cabinet.ID,
		current.SubjectID,
		contentapi.SubjectCharacteristicsQuery{Locale: contentapi.LocaleRU},
	)
	if err != nil {
		return nil, fmt.Errorf("получить характеристики категории: %w", err)
	}
	if schemaResponse.Error {
		return nil, responseError("получение характеристик", schemaResponse.ResponseMeta)
	}
	schema := make(map[string]contentapi.SubjectCharacteristic, len(schemaResponse.Data))
	for _, characteristic := range schemaResponse.Data {
		key := normalizeKey(characteristic.Name)
		if key != "" && characteristic.CharacteristicID > 0 {
			schema[key] = characteristic
		}
	}

	values := make(map[int64]any, len(current.Characteristics)+len(edited.Variant.Characteristics)+1)
	for _, characteristic := range current.Characteristics {
		if characteristic.ID > 0 {
			values[characteristic.ID] = characteristic.Value
		}
	}
	raw := append([]cardpipeline.RawCharacteristic(nil), edited.Variant.Characteristics...)
	if vat := strings.TrimSpace(edited.VATRate); vat != "" {
		raw = append(raw, cardpipeline.RawCharacteristic{Name: "Ставка НДС", Value: vat})
	}
	seen := make(map[int64]struct{}, len(raw))
	for _, characteristic := range raw {
		specification, exists := schema[normalizeKey(characteristic.Name)]
		if !exists {
			return nil, fmt.Errorf("характеристика %q отсутствует у категории %q", characteristic.Name, current.SubjectName)
		}
		if _, duplicate := seen[specification.CharacteristicID]; duplicate {
			return nil, fmt.Errorf("характеристика %q указана несколько раз", characteristic.Name)
		}
		seen[specification.CharacteristicID] = struct{}{}
		value, valueErr := editCharacteristicValue(specification, characteristic.Value)
		if valueErr != nil {
			return nil, fmt.Errorf("характеристика %q: %w", characteristic.Name, valueErr)
		}
		values[specification.CharacteristicID] = value
	}

	ids := make([]int64, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
	result := make([]contentapi.UploadCharacteristic, 0, len(ids))
	for _, id := range ids {
		result = append(result, contentapi.UploadCharacteristic{ID: id, Value: values[id]})
	}
	return result, nil
}

func editCharacteristicValue(
	specification contentapi.SubjectCharacteristic,
	raw any,
) (any, error) {
	switch specification.CharacteristicType {
	case 1:
		text, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("ожидалось текстовое значение")
		}
		parts := strings.Split(text, ";")
		values := make([]string, 0, len(parts))
		seen := make(map[string]struct{}, len(parts))
		for _, part := range parts {
			part = strings.TrimSpace(part)
			key := normalizeKey(part)
			if key == "" {
				return nil, fmt.Errorf("пустое значение")
			}
			if _, duplicate := seen[key]; duplicate {
				return nil, fmt.Errorf("значение %q повторяется", part)
			}
			seen[key] = struct{}{}
			values = append(values, part)
		}
		if specification.MaxCount > 0 && len(values) > specification.MaxCount {
			return nil, fmt.Errorf("допустимо не более %d значений", specification.MaxCount)
		}
		return values, nil
	case 4:
		text := strings.ReplaceAll(strings.TrimSpace(fmt.Sprint(raw)), ",", ".")
		value, err := strconv.ParseFloat(text, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("ожидалось число")
		}
		return value, nil
	case 0:
		return nil, fmt.Errorf("характеристика больше не поддерживается WB")
	default:
		return nil, fmt.Errorf("неподдерживаемый тип WB %d", specification.CharacteristicType)
	}
}

func (service *Service) recentWBErrors(
	ctx context.Context,
	cabinetID domain.CabinetID,
	vendorCode string,
	startedAt time.Time,
) ([]string, error) {
	response, err := service.gateway.CardsErrorList(
		ctx,
		cabinetID,
		contentapi.CardsErrorListQuery{Locale: contentapi.LocaleRU},
		contentapi.CardsErrorListRequest{
			Cursor: contentapi.CardsErrorListCursor{Limit: contentapi.MaxCardsErrorListPageSize},
			Order:  contentapi.CardsErrorListOrder{Ascending: false},
		},
	)
	if err != nil {
		return nil, err
	}
	if response.Error {
		return nil, responseError("проверка ошибок", response.ResponseMeta)
	}
	var result []string
	for _, batch := range response.Data.Items {
		updatedAt, parseErr := time.Parse(time.RFC3339Nano, batch.UpdatedAt)
		if parseErr != nil || updatedAt.Before(startedAt.Add(-5*time.Second)) {
			continue
		}
		for key, batchErrors := range batch.Errors {
			if strings.EqualFold(strings.TrimSpace(key), strings.TrimSpace(vendorCode)) {
				result = append(result, batchErrors...)
			}
		}
	}
	return result, nil
}

func updateConfirmed(
	observed contentapi.Card,
	expected contentapi.UpdateCard,
	media cardpipeline.Media,
	mediaChanged bool,
) bool {
	if observed.NMID != expected.NMID ||
		!equalText(observed.VendorCode, expected.VendorCode) ||
		!equalText(observed.Title, expected.Title) ||
		!equalText(observed.Description, expected.Description) ||
		!equalText(observed.Brand, expected.Brand) ||
		observed.KIZMarked != expected.KIZMarked {
		return false
	}
	if !mediaChanged {
		return true
	}
	if len(observed.Photos) < len(media.Photos) {
		return false
	}
	return strings.TrimSpace(media.Video) == "" || strings.TrimSpace(observed.Video) != ""
}

func equalText(left string, right string) bool {
	return strings.EqualFold(strings.Join(strings.Fields(left), " "), strings.Join(strings.Fields(right), " "))
}

func responseError(operation string, meta contentapi.ResponseMeta) error {
	details := strings.TrimSpace(meta.ErrorText)
	if meta.AdditionalErrors != nil {
		if encoded, err := json.Marshal(meta.AdditionalErrors); err == nil && string(encoded) != "null" && string(encoded) != "{}" {
			if details != "" {
				details += ": "
			}
			details += string(encoded)
		}
	}
	if details == "" {
		details = "неизвестная ошибка WB"
	}
	return fmt.Errorf("WB: %s: %s", operation, details)
}

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func parseOptionalBoolean(value string) (bool, bool) {
	switch normalizeKey(value) {
	case "нет", "no", "false", "0", "-":
		return false, true
	case "да", "yes", "true", "1", "+":
		return true, true
	default:
		return false, false
	}
}
