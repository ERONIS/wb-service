package cardimport_staging

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	cardimport_contract "github.com/ERONIS/wb-service/internal/feature/cardimport/service/contract"
	cardimport_model "github.com/ERONIS/wb-service/internal/feature/cardimport/service/model"
)

const terminalSessionRetention = 24 * time.Hour

type importFile struct {
	cardimport_model.File
	content []byte
	cards   []cardpipeline.Card
	issues  []cardimport_model.ParseIssue
}

type importSession struct {
	cardimport_model.Session
	files      map[cardimport_model.FileID]*importFile
	fileOrder  []cardimport_model.FileID
	cross      []cardimport_model.IssueView
	finalizing bool
	batch      cardimport_model.BatchHeader
}

type selectedCard struct {
	fileID cardimport_model.FileID
	card   cardpipeline.Card
}

type Store struct {
	mu       sync.Mutex
	prefix   uint64
	nextID   uint32
	sessions map[cardimport_model.SessionID]*importSession
	byAuthor map[int64]cardimport_model.SessionID
}

// NewStore создаёт хранилище незавершённых сессий в памяти.
func NewStore() *Store {
	var seed [4]byte
	if _, err := rand.Read(seed[:]); err != nil {
		panic(fmt.Sprintf("cardimport random ID source: %v", err))
	}
	return &Store{
		prefix:   uint64(binary.BigEndian.Uint32(seed[:])&0x3fffffff | 0x40000000),
		sessions: make(map[cardimport_model.SessionID]*importSession),
		byAuthor: make(map[int64]cardimport_model.SessionID),
	}
}

// Begin создаёт сессию или возвращает активную сессию автора.
func (store *Store) Begin(ctx context.Context, command cardimport_contract.BeginCommand) (cardimport_model.Session, error) {
	if err := ctx.Err(); err != nil {
		return cardimport_model.Session{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.pruneLocked(time.Now().UTC())
	if id := store.byAuthor[command.AuthorTelegramID]; id > 0 {
		return store.sessions[id].Session, nil
	}
	now := time.Now().UTC()
	session := cardimport_model.Session{
		ID: cardimport_model.SessionID(store.next()), AuthorTelegramID: command.AuthorTelegramID,
		Status:    cardimport_model.SessionStatusCollecting,
		CreatedAt: now, UpdatedAt: now,
	}
	store.sessions[session.ID] = &importSession{Session: session, files: make(map[cardimport_model.FileID]*importFile)}
	store.byAuthor[command.AuthorTelegramID] = session.ID
	return session, nil
}

// Reserve регистрирует файл в собираемой сессии.
func (store *Store) Reserve(ctx context.Context, command cardimport_contract.ReserveFileCommand) (cardimport_model.File, error) {
	if err := ctx.Err(); err != nil {
		return cardimport_model.File{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	session, err := store.collectingLocked(command.AuthorTelegramID, command.SessionID)
	if err != nil {
		return cardimport_model.File{}, err
	}
	for _, id := range session.fileOrder {
		file := session.files[id]
		if file.TelegramFileID == command.TelegramFileID ||
			file.TelegramFileUniqueID == command.TelegramFileUniqueID ||
			file.TelegramMessageID == command.TelegramMessageID {
			return file.File, nil
		}
	}
	if cardimport_model.MaxFilesPerSession > 0 && len(session.fileOrder) >= cardimport_model.MaxFilesPerSession {
		return cardimport_model.File{}, fmt.Errorf("cardimport file limit reached: %w", core_errors.ErrConflict)
	}
	now := time.Now().UTC()
	file := cardimport_model.File{
		ID: cardimport_model.FileID(store.next()), SessionID: command.SessionID,
		TelegramFileID: command.TelegramFileID, TelegramFileUniqueID: command.TelegramFileUniqueID,
		TelegramMessageID: command.TelegramMessageID, OriginalFilename: command.OriginalFilename,
		MIMEType: command.MIMEType, DeclaredSize: command.DeclaredSize,
		Status: cardimport_model.FileStatusReserved, CreatedAt: now, UpdatedAt: now,
	}
	session.files[file.ID] = &importFile{File: file}
	session.fileOrder = append(session.fileOrder, file.ID)
	return file, nil
}

// StoreFile сохраняет байты файла и его метаданные в памяти.
func (store *Store) StoreFile(ctx context.Context, command cardimport_contract.StoreFileCommand, content []byte) (cardimport_model.File, error) {
	if err := ctx.Err(); err != nil {
		return cardimport_model.File{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	session, err := store.collectingLocked(command.AuthorTelegramID, command.SessionID)
	if err != nil {
		return cardimport_model.File{}, err
	}
	file := session.files[command.FileID]
	if file == nil || file.Status != cardimport_model.FileStatusReserved && file.Status != cardimport_model.FileStatusStored {
		return cardimport_model.File{}, fmt.Errorf("cardimport file cannot be stored: %w", core_errors.ErrConflict)
	}
	if file.Status == cardimport_model.FileStatusStored && !bytes.Equal(file.content, content) {
		return cardimport_model.File{}, fmt.Errorf("cardimport stored content differs: %w", core_errors.ErrConflict)
	}
	file.content = content
	file.StoredSize = int64(len(content))
	file.Status = cardimport_model.FileStatusStored
	file.UpdatedAt = time.Now().UTC()
	return file.File, nil
}

// Claim помечает файл как разбираемый и выдаёт его содержимое.
func (store *Store) Claim(ctx context.Context, command cardimport_contract.ParseFileCommand) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	session, err := store.collectingLocked(command.AuthorTelegramID, command.SessionID)
	if err != nil {
		return nil, err
	}
	file := session.files[command.FileID]
	if file == nil || len(file.content) == 0 {
		return nil, fmt.Errorf("cardimport file content unavailable: %w", core_errors.ErrConflict)
	}
	if file.Status != cardimport_model.FileStatusStored && file.Status != cardimport_model.FileStatusValid && file.Status != cardimport_model.FileStatusInvalid &&
		!(file.Status == cardimport_model.FileStatusParsing && time.Since(file.UpdatedAt) >= 5*time.Minute) {
		return nil, fmt.Errorf("cardimport file cannot be parsed: %w", core_errors.ErrConflict)
	}
	file.Status = cardimport_model.FileStatusParsing
	file.UpdatedAt = time.Now().UTC()
	return file.content, nil
}

// SaveParsed сохраняет карточки и ошибки после разбора файла.
func (store *Store) SaveParsed(ctx context.Context, command cardimport_contract.ParseFileCommand, result cardimport_model.AggregatedFile) (cardimport_model.File, error) {
	if err := ctx.Err(); err != nil {
		return cardimport_model.File{}, err
	}
	if result.FileID != command.FileID {
		return cardimport_model.File{}, fmt.Errorf("parsed file ID differs from command: %w", core_errors.ErrConflict)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	session, err := store.collectingLocked(command.AuthorTelegramID, command.SessionID)
	if err != nil {
		return cardimport_model.File{}, err
	}
	file := session.files[command.FileID]
	if file == nil || file.Status != cardimport_model.FileStatusParsing {
		return cardimport_model.File{}, fmt.Errorf("cardimport file is not parsing: %w", core_errors.ErrConflict)
	}
	file.cards = append([]cardpipeline.Card(nil), result.Cards...)
	file.issues = append([]cardimport_model.ParseIssue(nil), result.Issues...)
	file.Status = cardimport_model.FileStatusValid
	if result.HasErrors() {
		file.Status = cardimport_model.FileStatusInvalid
	}
	file.UpdatedAt = time.Now().UTC()
	session.Revision++
	session.UpdatedAt = file.UpdatedAt
	session.rebuildCrossIssues()
	return file.File, nil
}

// Cancel отменяет сессию и освобождает её файлы.
func (store *Store) Cancel(ctx context.Context, command cardimport_contract.SessionCommand) (cardimport_model.Session, error) {
	if err := ctx.Err(); err != nil {
		return cardimport_model.Session{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	session, err := store.sessionLocked(command.AuthorTelegramID, command.SessionID)
	if err != nil {
		return cardimport_model.Session{}, err
	}
	if session.finalizing || session.Status == cardimport_model.SessionStatusFinalized {
		return cardimport_model.Session{}, fmt.Errorf("cardimport session cannot be cancelled: %w", core_errors.ErrConflict)
	}
	if session.Status == cardimport_model.SessionStatusCollecting {
		session.Status = cardimport_model.SessionStatusCancelled
		session.Revision++
		session.UpdatedAt = time.Now().UTC()
		delete(store.byAuthor, command.AuthorTelegramID)
		session.files = nil
		session.fileOrder = nil
		session.cross = nil
	}
	return session.Session, nil
}

// Get находит сессию по владельцу и ID.
func (store *Store) Get(ctx context.Context, command cardimport_contract.SessionCommand) (cardimport_model.Session, error) {
	if err := ctx.Err(); err != nil {
		return cardimport_model.Session{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	session, err := store.sessionLocked(command.AuthorTelegramID, command.SessionID)
	if err != nil {
		return cardimport_model.Session{}, err
	}
	return session.Session, nil
}

// ActiveView собирает представление активной сессии автора.
func (store *Store) ActiveView(ctx context.Context, authorID int64) (cardimport_model.SessionView, error) {
	if err := ctx.Err(); err != nil {
		return cardimport_model.SessionView{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	id := store.byAuthor[authorID]
	if id == 0 {
		return cardimport_model.SessionView{}, fmt.Errorf("collecting cardimport session: %w", core_errors.ErrNotFound)
	}
	return store.viewLocked(store.sessions[id]), nil
}

// View собирает представление указанной сессии.
func (store *Store) View(ctx context.Context, command cardimport_contract.SessionCommand) (cardimport_model.SessionView, error) {
	if err := ctx.Err(); err != nil {
		return cardimport_model.SessionView{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	session, err := store.sessionLocked(command.AuthorTelegramID, command.SessionID)
	if err != nil {
		return cardimport_model.SessionView{}, err
	}
	return store.viewLocked(session), nil
}

// Recoverable выбирает файлы для возобновления обработки.
func (store *Store) Recoverable(ctx context.Context, staleBefore, reparseBefore time.Time, limit int) ([]cardimport_contract.RecoverableFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	files := make([]cardimport_contract.RecoverableFile, 0)
	for _, session := range store.sessions {
		if session.Status != cardimport_model.SessionStatusCollecting || session.finalizing {
			continue
		}
		for _, id := range session.fileOrder {
			file := session.files[id]
			eligible := file.Status == cardimport_model.FileStatusReserved || file.Status == cardimport_model.FileStatusStored ||
				file.Status == cardimport_model.FileStatusParsing && !file.UpdatedAt.After(staleBefore) ||
				file.Status == cardimport_model.FileStatusInvalid && file.UpdatedAt.Before(reparseBefore) && hasIssueCode(file.issues, "price_invalid")
			if eligible {
				files = append(files, cardimport_contract.RecoverableFile{AuthorTelegramID: session.AuthorTelegramID, File: file.File})
			}
		}
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].File.UpdatedAt.Equal(files[j].File.UpdatedAt) {
			return files[i].File.ID < files[j].File.ID
		}
		return files[i].File.UpdatedAt.Before(files[j].File.UpdatedAt)
	})
	if len(files) > limit {
		files = files[:limit]
	}
	return files, nil
}

// viewLocked собирает счётчики, ошибки и готовность сессии.
func (store *Store) viewLocked(session *importSession) cardimport_model.SessionView {
	view := cardimport_model.SessionView{Session: session.Session, Files: make([]cardimport_model.FileView, 0), Issues: make([]cardimport_model.IssueView, 0)}
	allFinished := true
	for _, id := range session.fileOrder {
		file := session.files[id]
		fileView := cardimport_model.FileView{File: file.File, CardsCount: len(file.cards)}
		for _, issue := range file.issues {
			if issue.Severity == cardimport_model.IssueSeverityError {
				fileView.ErrorsCount++
				view.ErrorsCount++
			}
			view.Issues = append(view.Issues, cardimport_model.IssueView{FileID: id, Filename: file.OriginalFilename, Issue: issue})
		}
		view.Files = append(view.Files, fileView)
		view.CardsCount += fileView.CardsCount
		if file.Status != cardimport_model.FileStatusValid && file.Status != cardimport_model.FileStatusInvalid {
			allFinished = false
		}
	}
	view.ErrorsCount += len(session.cross)
	view.Issues = append(view.Issues, session.cross...)
	sort.SliceStable(view.Issues, func(i, j int) bool {
		return view.Issues[i].Issue.Severity == cardimport_model.IssueSeverityError && view.Issues[j].Issue.Severity != cardimport_model.IssueSeverityError
	})
	if len(view.Issues) > 20 {
		view.Issues = view.Issues[:20]
	}
	view.ReadyCardsCount = len(session.selectedCards())
	view.Ready = session.Status == cardimport_model.SessionStatusCollecting && allFinished && view.ReadyCardsCount > 0
	return view
}

// selectedCards отбирает корректные карточки без повторных артикулов и баркодов.
func (session *importSession) selectedCards() []selectedCard {
	candidates := make([]selectedCard, 0)
	seenVendors := make(map[string]struct{})
	for _, id := range session.fileOrder {
		file := session.files[id]
		if file.Status != cardimport_model.FileStatusValid && file.Status != cardimport_model.FileStatusInvalid {
			continue
		}
		for _, card := range file.cards {
			if cardHasBlockingIssue(card, file.issues) {
				continue
			}
			if _, exists := seenVendors[card.Variant.VendorCode]; exists {
				continue
			}
			seenVendors[card.Variant.VendorCode] = struct{}{}
			candidates = append(candidates, selectedCard{fileID: id, card: card})
		}
	}

	selected := make([]selectedCard, 0, len(candidates))
	seenBarcodes := make(map[string]struct{})
	for _, candidate := range candidates {
		barcodes := candidate.card.Barcodes()
		duplicate := false
		for _, barcode := range barcodes {
			if barcode == "" {
				continue
			}
			if _, exists := seenBarcodes[barcode]; exists {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		for _, barcode := range barcodes {
			if barcode != "" {
				seenBarcodes[barcode] = struct{}{}
			}
		}
		selected = append(selected, candidate)
	}
	return selected
}

// rebuildCrossIssues пересчитывает конфликты артикулов и баркодов между файлами.
func (session *importSession) rebuildCrossIssues() {
	session.cross = nil
	vendorFiles := make(map[string]cardimport_model.FileID)
	barcodeFiles := make(map[string]cardimport_model.FileID)
	seenVendor := make(map[string]struct{})
	seenBarcode := make(map[string]struct{})
	for _, id := range session.fileOrder {
		file := session.files[id]
		for _, card := range file.cards {
			code := card.Variant.VendorCode
			if previous, exists := vendorFiles[code]; exists && previous != id {
				if _, recorded := seenVendor[code]; !recorded {
					session.cross = append(session.cross, cardimport_model.IssueView{Issue: cardimport_model.ParseIssue{Severity: cardimport_model.IssueSeverityError, Code: "vendor_code_across_files", Message: fmt.Sprintf("Артикул продавца %q встречается в нескольких XLSX-файлах.", code)}})
					seenVendor[code] = struct{}{}
				}
			} else {
				vendorFiles[code] = id
			}
			for _, barcode := range card.Barcodes() {
				if previous, exists := barcodeFiles[barcode]; exists && previous != id {
					if _, recorded := seenBarcode[barcode]; !recorded {
						session.cross = append(session.cross, cardimport_model.IssueView{Issue: cardimport_model.ParseIssue{Severity: cardimport_model.IssueSeverityError, Code: "barcode_across_files", Message: fmt.Sprintf("Баркод %q встречается в нескольких XLSX-файлах.", barcode)}})
						seenBarcode[barcode] = struct{}{}
					}
				} else {
					barcodeFiles[barcode] = id
				}
			}
		}
	}
}

// collectingLocked находит сессию, доступную для изменения.
func (store *Store) collectingLocked(authorID int64, id cardimport_model.SessionID) (*importSession, error) {
	session, err := store.sessionLocked(authorID, id)
	if err != nil {
		return nil, err
	}
	if session.Status != cardimport_model.SessionStatusCollecting || session.finalizing {
		return nil, fmt.Errorf("cardimport session is not collecting: %w", core_errors.ErrConflict)
	}
	return session, nil
}

// sessionLocked находит сессию и сверяет владельца.
func (store *Store) sessionLocked(authorID int64, id cardimport_model.SessionID) (*importSession, error) {
	session := store.sessions[id]
	if session == nil || session.AuthorTelegramID != authorID {
		return nil, fmt.Errorf("cardimport session ID=%d: %w", id, core_errors.ErrNotFound)
	}
	return session, nil
}

// pruneLocked удаляет давно завершённые сессии из памяти.
func (store *Store) pruneLocked(now time.Time) {
	cutoff := now.Add(-terminalSessionRetention)
	for id, session := range store.sessions {
		if session.Status != cardimport_model.SessionStatusCollecting && session.UpdatedAt.Before(cutoff) {
			delete(store.sessions, id)
		}
	}
}

// next выдаёт следующий ID с префиксом процесса.
func (store *Store) next() int64 {
	store.nextID++
	if store.nextID == 0 {
		panic("cardimport in-memory ID space exhausted")
	}
	return int64(store.prefix<<32 | uint64(store.nextID))
}

// cardHasBlockingIssue проверяет ошибки, относящиеся к строкам карточки.
func cardHasBlockingIssue(card cardpipeline.Card, issues []cardimport_model.ParseIssue) bool {
	for _, issue := range issues {
		if issue.Severity != cardimport_model.IssueSeverityError {
			continue
		}
		if issue.Row <= 0 {
			return true
		}
		for _, row := range card.SourceRows {
			if row == issue.Row {
				return true
			}
		}
	}
	return false
}

// hasIssueCode ищет ошибку с указанным кодом.
func hasIssueCode(issues []cardimport_model.ParseIssue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}
