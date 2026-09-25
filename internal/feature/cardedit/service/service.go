package cardedit_service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/ERONIS/wb-service/internal/core/domain"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"

	"go.uber.org/zap"
)

const maxSearchPages = 500

type Service struct {
	ctx     context.Context
	gateway Gateway
	config  Config
	logger  *zap.Logger
	jobs    chan Job

	mu       sync.RWMutex
	notifier Notifier
	active   map[activeKey]struct{}
}

type activeKey struct {
	owner      int64
	vendorCode string
}

func New(
	ctx context.Context,
	gateway Gateway,
	config Config,
	logger *zap.Logger,
) *Service {
	if ctx == nil || gateway == nil || logger == nil {
		panic("cardedit service dependency is nil")
	}
	if err := config.Validate(); err != nil {
		panic(err)
	}
	service := &Service{
		ctx:     ctx,
		gateway: gateway,
		config:  config,
		logger:  logger,
		jobs:    make(chan Job, config.QueueSize),
		active:  make(map[activeKey]struct{}),
	}
	for worker := 0; worker < config.Workers; worker++ {
		go service.runWorker()
	}
	return service
}

func (service *Service) SetNotifier(notifier Notifier) {
	if notifier == nil {
		panic("cardedit notifier is nil")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.notifier != nil {
		panic("cardedit notifier is already configured")
	}
	service.notifier = notifier
}

// Find searches the exact seller article in every active cabinet currently
// owned by the actor. Any cabinet read failure aborts the selection so that a
// confirmation never silently covers only a subset of the owner's cabinets.
func (service *Service) Find(
	ctx context.Context,
	ownerTelegramID int64,
	vendorCode string,
) ([]Target, error) {
	vendorCode = strings.TrimSpace(vendorCode)
	if ctx == nil || ownerTelegramID <= 0 || vendorCode == "" {
		return nil, fmt.Errorf("find editable card: invalid argument")
	}
	cabinets, err := service.gateway.Cabinets(ctx, ownerTelegramID)
	if err != nil {
		return nil, fmt.Errorf("list editable cabinets: %w", err)
	}
	if len(cabinets) == 0 {
		return nil, nil
	}

	type searchResult struct {
		targets []Target
		err     error
	}
	results := make(chan searchResult, len(cabinets))
	tasks := make(chan Cabinet, len(cabinets))
	var wait sync.WaitGroup
	workers := min(service.config.Workers, len(cabinets))
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for cabinet := range tasks {
				cards, findErr := service.findCards(ctx, cabinet.ID, vendorCode, 0)
				if findErr != nil {
					results <- searchResult{err: fmt.Errorf("search cabinet %q: %w", cabinet.Name, findErr)}
					continue
				}
				targets := make([]Target, 0, len(cards))
				for _, card := range cards {
					if strings.EqualFold(strings.TrimSpace(card.VendorCode), vendorCode) {
						targets = append(targets, Target{Cabinet: cabinet, NMID: card.NMID})
					}
				}
				results <- searchResult{targets: targets}
			}
		}()
	}
	for _, cabinet := range cabinets {
		tasks <- cabinet
	}
	close(tasks)
	wait.Wait()
	close(results)

	var found []Target
	var searchErrors []error
	for result := range results {
		found = append(found, result.targets...)
		if result.err != nil {
			searchErrors = append(searchErrors, result.err)
		}
	}
	if len(searchErrors) > 0 {
		return nil, errors.Join(searchErrors...)
	}
	sort.Slice(found, func(left, right int) bool {
		if found[left].Cabinet.Name == found[right].Cabinet.Name {
			return found[left].NMID < found[right].NMID
		}
		return found[left].Cabinet.Name < found[right].Cabinet.Name
	})
	return found, nil
}

func (service *Service) Submit(job Job) error {
	job.VendorCode = strings.TrimSpace(job.VendorCode)
	if job.OwnerTelegramID <= 0 || job.VendorCode == "" || len(job.Targets) == 0 {
		return fmt.Errorf("submit card edit: invalid argument")
	}
	if !strings.EqualFold(strings.TrimSpace(job.Card.Variant.VendorCode), job.VendorCode) {
		return fmt.Errorf("submit card edit: Excel vendor code differs from selected card")
	}
	seenTargets := make(map[string]struct{}, len(job.Targets))
	for _, target := range job.Targets {
		if target.Cabinet.ID == "" || target.Cabinet.Name == "" ||
			target.Cabinet.BindingRevision <= 0 ||
			target.Cabinet.CapabilityRevision <= 0 ||
			target.Cabinet.ClientGeneration == (domain.ClientGeneration{}) || target.NMID <= 0 {
			return fmt.Errorf("submit card edit: invalid target")
		}
		targetKey := string(target.Cabinet.ID) + ":" + strconv.FormatInt(target.NMID, 10)
		if _, duplicate := seenTargets[targetKey]; duplicate {
			return fmt.Errorf("submit card edit: duplicate target")
		}
		seenTargets[targetKey] = struct{}{}
	}
	job.Targets = append([]Target(nil), job.Targets...)
	key := activeKey{owner: job.OwnerTelegramID, vendorCode: normalizeKey(job.VendorCode)}
	service.mu.Lock()
	if service.notifier == nil {
		service.mu.Unlock()
		return errors.New("cardedit notifier is not configured")
	}
	if _, exists := service.active[key]; exists {
		service.mu.Unlock()
		return ErrAlreadyActive
	}
	service.active[key] = struct{}{}
	service.mu.Unlock()

	select {
	case <-service.ctx.Done():
		service.clearActive(key)
		return service.ctx.Err()
	case service.jobs <- job:
		return nil
	default:
		service.clearActive(key)
		return ErrQueueFull
	}
}

func (service *Service) runWorker() {
	for {
		select {
		case <-service.ctx.Done():
			return
		case job := <-service.jobs:
			service.process(job)
		}
	}
}

func (service *Service) process(job Job) {
	key := activeKey{owner: job.OwnerTelegramID, vendorCode: normalizeKey(job.VendorCode)}
	defer service.clearActive(key)

	result := Result{
		VendorCode: job.VendorCode,
		Targets:    make([]TargetResult, len(job.Targets)),
	}
	tasks := make(chan int, len(job.Targets))
	var wait sync.WaitGroup
	workers := min(service.config.Workers, len(job.Targets))
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := range tasks {
				result.Targets[index] = service.processTarget(job, job.Targets[index])
			}
		}()
	}
	for index := range job.Targets {
		tasks <- index
	}
	close(tasks)
	wait.Wait()

	service.mu.RLock()
	notifier := service.notifier
	service.mu.RUnlock()
	if notifier != nil {
		if err := notifier.NotifyEditResult(job.OwnerTelegramID, result); err != nil {
			service.logger.Warn("notify card edit result", zap.Error(err), zap.Int64("telegram_id", job.OwnerTelegramID))
		}
	}
}

func (service *Service) clearActive(key activeKey) {
	service.mu.Lock()
	delete(service.active, key)
	service.mu.Unlock()
}

func (service *Service) findCards(
	ctx context.Context,
	cabinetID domain.CabinetID,
	textSearch string,
	nmID int64,
) ([]contentapi.Card, error) {
	cursor := contentapi.CardsListCursor{Limit: contentapi.MaxCardsListPageSize}
	result := make([]contentapi.Card, 0, 1)
	seen := make(map[int64]struct{})
	for page := 0; page < maxSearchPages; page++ {
		response, err := service.gateway.CardsList(
			ctx,
			cabinetID,
			contentapi.CardsListQuery{Locale: contentapi.LocaleRU},
			contentapi.CardsListRequest{Settings: contentapi.CardsListSettings{
				Sort:   contentapi.CardsSort{Ascending: true},
				Filter: &contentapi.CardsListFilter{TextSearch: textSearch},
				Cursor: cursor,
			}},
		)
		if err != nil {
			return nil, err
		}
		for _, card := range response.Cards {
			if nmID > 0 && card.NMID != nmID {
				continue
			}
			if _, exists := seen[card.NMID]; exists {
				continue
			}
			seen[card.NMID] = struct{}{}
			result = append(result, card)
		}
		if len(response.Cards) < contentapi.MaxCardsListPageSize {
			return result, nil
		}
		next := contentapi.CardsListCursor{
			UpdatedAt: response.Cursor.UpdatedAt,
			NMID:      response.Cursor.NMID,
			Limit:     contentapi.MaxCardsListPageSize,
		}
		if next.UpdatedAt == cursor.UpdatedAt && next.NMID == cursor.NMID {
			return nil, errors.New("WB cards cursor did not advance")
		}
		cursor = next
	}
	return nil, fmt.Errorf("WB cards search exceeded %d pages", maxSearchPages)
}

func (service *Service) findCardByNMID(
	ctx context.Context,
	target Target,
) (contentapi.Card, error) {
	cards, err := service.findCards(
		ctx,
		target.Cabinet.ID,
		strconv.FormatInt(target.NMID, 10),
		target.NMID,
	)
	if err != nil {
		return contentapi.Card{}, err
	}
	if len(cards) != 1 {
		return contentapi.Card{}, ErrCardNotFound
	}
	return cards[0], nil
}

func normalizeKey(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}
