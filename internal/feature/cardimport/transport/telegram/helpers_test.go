package cardimport_telegram_transport

import (
	"fmt"
	"strings"
	"testing"
	"time"

	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
)

func TestSessionViewTextLimitsDisplayedFiles(t *testing.T) {
	t.Parallel()

	files := make([]cardimport_service.FileView, sessionViewFilesLimit+3)
	for index := range files {
		files[index].OriginalFilename = fmt.Sprintf("file-%02d.xlsx", index+1)
	}
	view := cardimport_service.SessionView{Files: files}

	actual := sessionViewText(view)
	if !strings.Contains(actual, "…предыдущих файлов: 3") {
		t.Fatalf("sessionViewText() does not report hidden files: %q", actual)
	}
	if strings.Contains(actual, "file-03.xlsx") {
		t.Fatalf("sessionViewText() contains a hidden file: %q", actual)
	}
	if !strings.Contains(actual, "file-04.xlsx") ||
		!strings.Contains(actual, "file-13.xlsx") {
		t.Fatalf("sessionViewText() does not contain the latest files: %q", actual)
	}
}

func TestRecoverableFilesCount(t *testing.T) {
	t.Parallel()

	now := time.Now()
	view := cardimport_service.SessionView{Files: []cardimport_service.FileView{
		{File: cardimport_service.File{Status: cardimport_service.FileStatusReserved}},
		{File: cardimport_service.File{Status: cardimport_service.FileStatusStored}},
		{File: cardimport_service.File{
			Status:    cardimport_service.FileStatusParsing,
			UpdatedAt: now.Add(-staleParsingFileAge),
		}},
		{File: cardimport_service.File{
			Status:    cardimport_service.FileStatusParsing,
			UpdatedAt: now,
		}},
		{File: cardimport_service.File{Status: cardimport_service.FileStatusValid}},
	}}

	if actual := recoverableFilesCount(view, now); actual != 3 {
		t.Fatalf("recoverableFilesCount() = %d, want 3", actual)
	}
}

func TestSessionViewTextHidesDuplicateIssuesFromFirstErrors(t *testing.T) {
	t.Parallel()

	view := cardimport_service.SessionView{Issues: []cardimport_service.IssueView{
		{Issue: cardimport_service.ParseIssue{
			Code:    "vendor_code_across_files",
			Message: "duplicate vendor marker",
		}},
		{Issue: cardimport_service.ParseIssue{
			Code:    "barcode_across_files",
			Message: "duplicate barcode marker",
		}},
		{Issue: cardimport_service.ParseIssue{
			Code:    "price_invalid",
			Message: "visible price marker",
		}},
	}}

	actual := sessionViewText(view)
	if strings.Contains(actual, "duplicate vendor marker") ||
		strings.Contains(actual, "duplicate barcode marker") {
		t.Fatalf("sessionViewText() exposes duplicate issues: %q", actual)
	}
	if !strings.Contains(actual, "visible price marker") {
		t.Fatalf("sessionViewText() hides a regular issue: %q", actual)
	}
}
