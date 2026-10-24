package workflow

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

const terminalSessionRetention = 24 * time.Hour

type importFile struct {
	File
	content []byte
	cards   []AggregatedCard
	issues  []ParseIssue
}

type importSession struct {
	Session
	files      map[FileID]*importFile
	fileOrder  []FileID
	cross      []IssueView
	finalizing bool
	batch      BatchHeader
	command    Digest
}

type sessionStore struct {
	mu       sync.Mutex
	prefix   uint64
	nextID   uint32
	sessions map[SessionID]*importSession
	byAuthor map[int64]SessionID
}

func newSessionStore() *sessionStore {
	var seed [4]byte
	if _, err := rand.Read(seed[:]); err != nil {
		panic(fmt.Sprintf("cardimport random ID source: %v", err))
	}
	return &sessionStore{
		prefix:   uint64(binary.BigEndian.Uint32(seed[:])&0x3fffffff | 0x40000000),
		sessions: make(map[SessionID]*importSession),
		byAuthor: make(map[int64]SessionID),
	}
}

func (store *sessionStore) next() int64 {
	store.nextID++
	if store.nextID == 0 {
		panic("cardimport in-memory ID space exhausted")
	}
	return int64(store.prefix<<32 | uint64(store.nextID))
}

func (store *sessionStore) begin(ctx context.Context, command BeginCommand) (Session, error) {
	if err := ctx.Err(); err != nil {
		return Session{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.pruneLocked(time.Now().UTC())
	if id := store.byAuthor[command.AuthorTelegramID]; id > 0 {
		return store.sessions[id].Session, nil
	}
	now := time.Now().UTC()
	session := Session{
		ID: SessionID(store.next()), AuthorTelegramID: command.AuthorTelegramID,
		Purpose: command.Purpose, Status: SessionStatusCollecting,
		CreatedAt: now, UpdatedAt: now,
	}
	store.sessions[session.ID] = &importSession{Session: session, files: make(map[FileID]*importFile)}
	store.byAuthor[command.AuthorTelegramID] = session.ID
	return session, nil
}

func (store *sessionStore) get(ctx context.Context, command SessionCommand) (Session, error) {
	if err := ctx.Err(); err != nil {
		return Session{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	session, err := store.sessionLocked(command.AuthorTelegramID, command.SessionID)
	if err != nil {
		return Session{}, err
	}
	return session.Session, nil
}

func (store *sessionStore) activeView(ctx context.Context, authorID int64) (SessionView, error) {
	if err := ctx.Err(); err != nil {
		return SessionView{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	id := store.byAuthor[authorID]
	if id == 0 {
		return SessionView{}, fmt.Errorf("collecting cardimport session: %w", core_errors.ErrNotFound)
	}
	return store.viewLocked(store.sessions[id]), nil
}

func (store *sessionStore) view(ctx context.Context, command SessionCommand) (SessionView, error) {
	if err := ctx.Err(); err != nil {
		return SessionView{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	session, err := store.sessionLocked(command.AuthorTelegramID, command.SessionID)
	if err != nil {
		return SessionView{}, err
	}
	return store.viewLocked(session), nil
}

func (store *sessionStore) cancel(ctx context.Context, command SessionCommand) (Session, error) {
	if err := ctx.Err(); err != nil {
		return Session{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	session, err := store.sessionLocked(command.AuthorTelegramID, command.SessionID)
	if err != nil {
		return Session{}, err
	}
	if session.finalizing || session.Status == SessionStatusFinalized {
		return Session{}, fmt.Errorf("cardimport session cannot be cancelled: %w", core_errors.ErrConflict)
	}
	if session.Status == SessionStatusCollecting {
		session.Status = SessionStatusCancelled
		session.Revision++
		session.UpdatedAt = time.Now().UTC()
		delete(store.byAuthor, command.AuthorTelegramID)
		session.files = nil
		session.fileOrder = nil
		session.cross = nil
	}
	return session.Session, nil
}

func (store *sessionStore) reserve(ctx context.Context, command ReserveFileCommand) (File, error) {
	if err := ctx.Err(); err != nil {
		return File{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	session, err := store.collectingLocked(command.AuthorTelegramID, command.SessionID)
	if err != nil {
		return File{}, err
	}
	for _, id := range session.fileOrder {
		file := session.files[id]
		if file.TelegramFileID == command.TelegramFileID ||
			file.TelegramFileUniqueID == command.TelegramFileUniqueID ||
			file.TelegramMessageID == command.TelegramMessageID {
			return file.File, nil
		}
	}
	if MaxFilesPerSession > 0 && len(session.fileOrder) >= MaxFilesPerSession {
		return File{}, fmt.Errorf("cardimport file limit reached: %w", core_errors.ErrConflict)
	}
	now := time.Now().UTC()
	file := File{
		ID: FileID(store.next()), SessionID: command.SessionID,
		TelegramFileID: command.TelegramFileID, TelegramFileUniqueID: command.TelegramFileUniqueID,
		TelegramMessageID: command.TelegramMessageID, OriginalFilename: command.OriginalFilename,
		MIMEType: command.MIMEType, DeclaredSize: command.DeclaredSize,
		Status: FileStatusReserved, CreatedAt: now, UpdatedAt: now,
	}
	session.files[file.ID] = &importFile{File: file}
	session.fileOrder = append(session.fileOrder, file.ID)
	return file, nil
}

func (store *sessionStore) store(ctx context.Context, command StoreFileCommand, content StoredContent) (File, error) {
	if err := ctx.Err(); err != nil {
		return File{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	session, err := store.collectingLocked(command.AuthorTelegramID, command.SessionID)
	if err != nil {
		return File{}, err
	}
	file := session.files[command.FileID]
	if file == nil || file.Status != FileStatusReserved && file.Status != FileStatusStored {
		return File{}, fmt.Errorf("cardimport file cannot be stored: %w", core_errors.ErrConflict)
	}
	if file.Status == FileStatusStored && !bytes.Equal(file.content, content.Bytes) {
		return File{}, fmt.Errorf("cardimport stored content differs: %w", core_errors.ErrConflict)
	}
	file.content = content.Bytes
	file.StoredSize = int64(len(content.Bytes))
	file.SHA256 = hex.EncodeToString(content.SHA256[:])
	file.Status = FileStatusStored
	file.UpdatedAt = time.Now().UTC()
	return file.File, nil
}

func (store *sessionStore) claim(ctx context.Context, command ParseFileCommand) ([]byte, error) {
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
	if file.Status != FileStatusStored && file.Status != FileStatusValid && file.Status != FileStatusInvalid &&
		!(file.Status == FileStatusParsing && time.Since(file.UpdatedAt) >= 5*time.Minute) {
		return nil, fmt.Errorf("cardimport file cannot be parsed: %w", core_errors.ErrConflict)
	}
	file.Status = FileStatusParsing
	file.UpdatedAt = time.Now().UTC()
	return file.content, nil
}

func (store *sessionStore) saveParsed(ctx context.Context, command ParseFileCommand, result AggregatedFile) (File, error) {
	if err := ctx.Err(); err != nil {
		return File{}, err
	}
	if result.FileID != command.FileID {
		return File{}, fmt.Errorf("parsed file ID differs from command: %w", core_errors.ErrConflict)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	session, err := store.collectingLocked(command.AuthorTelegramID, command.SessionID)
	if err != nil {
		return File{}, err
	}
	file := session.files[command.FileID]
	if file == nil || file.Status != FileStatusParsing {
		return File{}, fmt.Errorf("cardimport file is not parsing: %w", core_errors.ErrConflict)
	}
	file.cards = append([]AggregatedCard(nil), result.Cards...)
	file.issues = append([]ParseIssue(nil), result.Issues...)
	file.Status = FileStatusValid
	if result.HasErrors() {
		file.Status = FileStatusInvalid
	}
	file.UpdatedAt = time.Now().UTC()
	session.Revision++
	session.UpdatedAt = file.UpdatedAt
	session.rebuildCrossIssues()
	return file.File, nil
}

func (store *sessionStore) continueAfterErrors(ctx context.Context, command ContinueCommand) (Session, error) {
	if err := ctx.Err(); err != nil {
		return Session{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	session, err := store.collectingLocked(command.AuthorTelegramID, command.SessionID)
	if err != nil {
		return Session{}, err
	}
	if session.Revision != command.ExpectedRevision || !session.hasErrors() {
		return Session{}, fmt.Errorf("cardimport session is stale or has no errors: %w", core_errors.ErrConflict)
	}
	seenVendor := make(map[string]struct{})
	for _, id := range session.fileOrder {
		file := session.files[id]
		if file.Status != FileStatusValid && file.Status != FileStatusInvalid {
			continue
		}
		kept := make([]AggregatedCard, 0, len(file.cards))
		for _, card := range file.cards {
			if cardHasBlockingIssue(card, file.issues) {
				continue
			}
			if _, exists := seenVendor[card.Variant.VendorCode]; exists {
				continue
			}
			seenVendor[card.Variant.VendorCode] = struct{}{}
			kept = append(kept, card)
		}
		file.cards = kept
	}
	seenBarcode := make(map[string]struct{})
	for _, id := range session.fileOrder {
		file := session.files[id]
		if file.Status != FileStatusValid && file.Status != FileStatusInvalid {
			continue
		}
		kept := make([]AggregatedCard, 0, len(file.cards))
		for _, card := range file.cards {
			duplicate := false
			for _, barcode := range card.Barcodes() {
				if _, exists := seenBarcode[barcode]; exists {
					duplicate = true
				}
				seenBarcode[barcode] = struct{}{}
			}
			if !duplicate {
				kept = append(kept, card)
			}
		}
		file.cards = kept
		file.issues = retainWarnings(file.issues)
		file.Status = FileStatusValid
		if len(kept) == 0 {
			file.Status = FileStatusAbandoned
			file.issues = nil
			file.content = nil
		}
		file.UpdatedAt = time.Now().UTC()
	}
	session.Revision++
	session.UpdatedAt = time.Now().UTC()
	session.rebuildCrossIssues()
	return session.Session, nil
}

func (store *sessionStore) recoverable(ctx context.Context, staleBefore, reparseBefore time.Time, limit int) ([]RecoverableFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	files := make([]RecoverableFile, 0)
	for _, session := range store.sessions {
		if session.Status != SessionStatusCollecting || session.finalizing {
			continue
		}
		for _, id := range session.fileOrder {
			file := session.files[id]
			eligible := file.Status == FileStatusReserved || file.Status == FileStatusStored ||
				file.Status == FileStatusParsing && !file.UpdatedAt.After(staleBefore) ||
				file.Status == FileStatusInvalid && file.UpdatedAt.Before(reparseBefore) && hasIssueCode(file.issues, "price_invalid")
			if eligible {
				files = append(files, RecoverableFile{AuthorTelegramID: session.AuthorTelegramID, File: file.File})
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

func (store *sessionStore) sessionLocked(authorID int64, id SessionID) (*importSession, error) {
	session := store.sessions[id]
	if session == nil || session.AuthorTelegramID != authorID {
		return nil, fmt.Errorf("cardimport session ID=%d: %w", id, core_errors.ErrNotFound)
	}
	return session, nil
}

func (store *sessionStore) pruneLocked(now time.Time) {
	cutoff := now.Add(-terminalSessionRetention)
	for id, session := range store.sessions {
		if session.Status != SessionStatusCollecting && session.UpdatedAt.Before(cutoff) {
			delete(store.sessions, id)
		}
	}
}

func (store *sessionStore) collectingLocked(authorID int64, id SessionID) (*importSession, error) {
	session, err := store.sessionLocked(authorID, id)
	if err != nil {
		return nil, err
	}
	if session.Status != SessionStatusCollecting || session.finalizing {
		return nil, fmt.Errorf("cardimport session is not collecting: %w", core_errors.ErrConflict)
	}
	return session, nil
}

func (store *sessionStore) viewLocked(session *importSession) SessionView {
	view := SessionView{Session: session.Session, Files: make([]FileView, 0), Issues: make([]IssueView, 0)}
	allValid := true
	for _, id := range session.fileOrder {
		file := session.files[id]
		if file.Status == FileStatusAbandoned {
			continue
		}
		fileView := FileView{File: file.File, CardsCount: len(file.cards)}
		for _, issue := range file.issues {
			if issue.Severity == IssueSeverityError {
				fileView.ErrorsCount++
				view.ErrorsCount++
			}
			if issue.Code != "duplicate_barcode_in_file" {
				view.Issues = append(view.Issues, IssueView{FileID: id, Filename: file.OriginalFilename, Issue: issue})
			}
		}
		view.Files = append(view.Files, fileView)
		view.CardsCount += fileView.CardsCount
		if file.Status != FileStatusValid {
			allValid = false
		}
	}
	view.ErrorsCount += len(session.cross)
	sort.SliceStable(view.Issues, func(i, j int) bool {
		return view.Issues[i].Issue.Severity == IssueSeverityError && view.Issues[j].Issue.Severity != IssueSeverityError
	})
	if len(view.Issues) > 20 {
		view.Issues = view.Issues[:20]
	}
	view.Ready = session.Status == SessionStatusCollecting && len(view.Files) > 0 && allValid && view.ErrorsCount == 0
	return view
}

func (session *importSession) hasErrors() bool {
	if len(session.cross) > 0 {
		return true
	}
	for _, file := range session.files {
		for _, issue := range file.issues {
			if issue.Severity == IssueSeverityError {
				return true
			}
		}
	}
	return false
}

func (session *importSession) rebuildCrossIssues() {
	session.cross = nil
	vendorFiles := make(map[string]FileID)
	barcodeFiles := make(map[string]FileID)
	seenVendor := make(map[string]struct{})
	seenBarcode := make(map[string]struct{})
	for _, id := range session.fileOrder {
		file := session.files[id]
		if file.Status == FileStatusAbandoned {
			continue
		}
		for _, card := range file.cards {
			code := card.Variant.VendorCode
			if previous, exists := vendorFiles[code]; exists && previous != id {
				if _, recorded := seenVendor[code]; !recorded {
					session.cross = append(session.cross, IssueView{Issue: ParseIssue{Severity: IssueSeverityError, Code: "vendor_code_across_files", Message: fmt.Sprintf("Артикул продавца %q встречается в нескольких XLSX-файлах.", code)}})
					seenVendor[code] = struct{}{}
				}
			} else {
				vendorFiles[code] = id
			}
			for _, barcode := range card.Barcodes() {
				if previous, exists := barcodeFiles[barcode]; exists && previous != id {
					if _, recorded := seenBarcode[barcode]; !recorded {
						session.cross = append(session.cross, IssueView{Issue: ParseIssue{Severity: IssueSeverityError, Code: "barcode_across_files", Message: fmt.Sprintf("Баркод %q встречается в нескольких XLSX-файлах.", barcode)}})
						seenBarcode[barcode] = struct{}{}
					}
				} else {
					barcodeFiles[barcode] = id
				}
			}
		}
	}
}

func cardHasBlockingIssue(card AggregatedCard, issues []ParseIssue) bool {
	for _, issue := range issues {
		if issue.Severity != IssueSeverityError {
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

func retainWarnings(issues []ParseIssue) []ParseIssue {
	kept := make([]ParseIssue, 0, len(issues))
	for _, issue := range issues {
		if issue.Severity != IssueSeverityError {
			kept = append(kept, issue)
		}
	}
	return kept
}

func hasIssueCode(issues []ParseIssue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}
