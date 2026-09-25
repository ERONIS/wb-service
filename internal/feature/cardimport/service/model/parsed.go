package model

import "github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"

type IssueSeverity string

const (
	IssueSeverityError   IssueSeverity = "error"
	IssueSeverityWarning IssueSeverity = "warning"
)

type ParseIssue struct {
	Severity  IssueSeverity `json:"severity"`
	Code      string        `json:"code"`
	Message   string        `json:"message"`
	SheetName string        `json:"sheetName"`
	Row       int           `json:"row"`
	Column    string        `json:"column"`
}

type ParsedFile struct {
	FileID    FileID
	SheetName string
	HeaderRow int
	Rows      []ParsedRow
	Issues    []ParseIssue
}

func (f ParsedFile) HasErrors() bool {
	for _, issue := range f.Issues {
		if issue.Severity == IssueSeverityError {
			return true
		}
	}

	return false
}

// ParsedRow сохраняет одну исходную товарную строку XLSX без межстрочного
// объединения. Group имеет область только текущего FileID.
type ParsedRow struct {
	SourceRow int    `json:"sourceRow"`
	Group     string `json:"group"`
	Category  string `json:"category"`
	Price     int64  `json:"price"`

	KIZRequirement   string `json:"kizRequirement"`
	AdultOnly        string `json:"adultOnly"`
	MarkingConfirmed string `json:"markingConfirmed"`
	VATRate          string `json:"vatRate"`

	Variant ParsedVariant `json:"variant"`
	Media   ParsedMedia   `json:"media"`
}

type ParsedVariant = cardpipeline.Variant
type ParsedDimensions = cardpipeline.Dimensions
type ParsedSize = cardpipeline.Size
type RawCharacteristic = cardpipeline.RawCharacteristic
type ParsedMedia = cardpipeline.Media

// AggregatedCard представляет одну карточку после объединения всех строк с
// одинаковым артикулом продавца внутри одного XLSX-файла.
type AggregatedCard = cardpipeline.Card

type AggregatedFile struct {
	FileID    FileID
	SheetName string
	Cards     []AggregatedCard
	Issues    []ParseIssue
}

func (f AggregatedFile) HasErrors() bool {
	for _, issue := range f.Issues {
		if issue.Severity == IssueSeverityError {
			return true
		}
	}

	return false
}
