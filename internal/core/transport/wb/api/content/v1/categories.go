package v1

// Locale задаёт язык локализуемых полей Content API.
type Locale string

const (
	LocaleRU Locale = "ru"
	LocaleEN Locale = "en"
	LocaleZH Locale = "zh"
)

type ParentCategoriesQuery struct {
	Locale Locale `url:"locale,omitempty"`
}

type ParentCategoriesResponse struct {
	Data []ParentCategory `json:"data"`
	ResponseMeta
}

type ParentCategory struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	IsVisible bool   `json:"isVisible"`
}

type SubjectsQuery struct {
	Locale   Locale `url:"locale,omitempty"`
	Name     string `url:"name,omitempty"`
	Limit    int    `url:"limit,omitempty"`
	Offset   int    `url:"offset,omitempty"`
	ParentID int64  `url:"parentID,omitempty"`
}

type SubjectsResponse struct {
	Data []Subject `json:"data"`
	ResponseMeta
}

type Subject struct {
	SubjectID   int64  `json:"subjectID"`
	ParentID    int64  `json:"parentID"`
	SubjectName string `json:"subjectName"`
	ParentName  string `json:"parentName"`
}

type SubjectCharacteristicsQuery struct {
	Locale Locale `url:"locale,omitempty"`
}

type SubjectCharacteristicsResponse struct {
	Data []SubjectCharacteristic `json:"data"`
	ResponseMeta
}

type SubjectCharacteristic struct {
	CharacteristicID   int64  `json:"charcID"`
	SubjectName        string `json:"subjectName"`
	SubjectID          int64  `json:"subjectID"`
	Name               string `json:"name"`
	Required           bool   `json:"required"`
	HasFilter          bool   `json:"hasFilter"`
	UnitName           string `json:"unitName"`
	MaxCount           int    `json:"maxCount"`
	Popular            bool   `json:"popular"`
	CharacteristicType int    `json:"charcType"`
}

type BrandsQuery struct {
	SubjectID int64 `url:"subjectId"`
	Next      int64 `url:"next,omitempty"`
}

type BrandsResponse struct {
	Brands []Brand `json:"brands"`
	Next   int64   `json:"next"`
	Total  int64   `json:"total"`
}

type Brand struct {
	ID      int64  `json:"id"`
	LogoURL string `json:"logoUrl"`
	Name    string `json:"name"`
}
