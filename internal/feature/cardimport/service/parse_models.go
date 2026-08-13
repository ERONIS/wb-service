package cardimport_service

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

type ParsedVariant struct {
	VendorCode      string              `json:"vendorCode"`
	Title           string              `json:"title"`
	Description     string              `json:"description"`
	Brand           string              `json:"brand"`
	Dimensions      ParsedDimensions    `json:"dimensions"`
	Sizes           []ParsedSize        `json:"sizes"`
	Characteristics []RawCharacteristic `json:"characteristics"`
}

type ParsedDimensions struct {
	Length       float64 `json:"length"`
	Width        float64 `json:"width"`
	Height       float64 `json:"height"`
	WeightBrutto float64 `json:"weightBrutto"`
}

type ParsedSize struct {
	TechSize string   `json:"techSize"`
	WBSize   string   `json:"wbSize"`
	SKUs     []string `json:"skus"`
}

type RawCharacteristic struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type ParsedMedia struct {
	Photos []string `json:"photos"`
	Video  string   `json:"video"`
}

// AggregatedCard представляет одну карточку после объединения всех строк с
// одинаковым артикулом продавца внутри одного XLSX-файла.
type AggregatedCard struct {
	SourceRows []int  `json:"sourceRows"`
	Group      string `json:"group"`
	Category   string `json:"category"`
	Price      int64  `json:"price"`

	KIZRequirement   string `json:"kizRequirement"`
	AdultOnly        string `json:"adultOnly"`
	MarkingConfirmed string `json:"markingConfirmed"`
	VATRate          string `json:"vatRate"`

	Variant ParsedVariant `json:"variant"`
	Media   ParsedMedia   `json:"media"`
}

func (c AggregatedCard) Barcodes() []string {
	barcodes := make([]string, 0)
	for _, size := range c.Variant.Sizes {
		barcodes = append(barcodes, size.SKUs...)
	}

	return barcodes
}

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
