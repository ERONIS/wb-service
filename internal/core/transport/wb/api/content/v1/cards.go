package v1

type CardsLimitsResponse struct {
	Data CardsLimits `json:"data"`
	ResponseMeta
}

type CardsLimits struct {
	FreeLimits int64 `json:"freeLimits"`
	PaidLimits int64 `json:"paidLimits"`
}

type CardsListQuery struct {
	Locale Locale `url:"locale,omitempty"`
}

type CardsListRequest struct {
	Settings CardsListSettings `json:"settings"`
}

type CardsListSettings struct {
	Sort   CardsSort        `json:"sort"`
	Filter *CardsListFilter `json:"filter,omitempty"`
	Cursor CardsListCursor  `json:"cursor"`
}

type CardsSort struct {
	Ascending bool `json:"ascending"`
}

type CardsListFilter struct {
	TextSearch            string   `json:"textSearch,omitempty"`
	AllowedCategoriesOnly bool     `json:"allowedCategoriesOnly,omitempty"`
	TagIDs                []int64  `json:"tagIDs,omitempty"`
	ObjectIDs             []int64  `json:"objectIDs,omitempty"`
	Brands                []string `json:"brands,omitempty"`
	IMTID                 int64    `json:"imtID,omitempty"`
	WithPhoto             *int     `json:"withPhoto,omitempty"`
}

type CardsListCursor struct {
	UpdatedAt string `json:"updatedAt,omitempty"`
	NMID      int64  `json:"nmID,omitempty"`
	Limit     int    `json:"limit"`
}

type CardsListResponse struct {
	Cards  []Card              `json:"cards"`
	Cursor CardsResponseCursor `json:"cursor"`
}

type CardsResponseCursor struct {
	UpdatedAt string `json:"updatedAt"`
	NMID      int64  `json:"nmID"`
	Total     int    `json:"total"`
}

type Card struct {
	NMID            int64                `json:"nmID"`
	IMTID           int64                `json:"imtID"`
	NMUUID          string               `json:"nmUUID"`
	SubjectID       int64                `json:"subjectID"`
	SubjectName     string               `json:"subjectName"`
	VendorCode      string               `json:"vendorCode"`
	KIZMarked       bool                 `json:"kizMarked"`
	Brand           string               `json:"brand"`
	Title           string               `json:"title"`
	Description     string               `json:"description"`
	NeedKIZ         bool                 `json:"needKiz"`
	Photos          []CardPhoto          `json:"photos"`
	Video           string               `json:"video"`
	Wholesale       CardWholesale        `json:"wholesale"`
	Dimensions      CardDimensions       `json:"dimensions"`
	Characteristics []CardCharacteristic `json:"characteristics"`
	Sizes           []CardSize           `json:"sizes"`
	Tags            []CardTag            `json:"tags"`
	CreatedAt       string               `json:"createdAt"`
	UpdatedAt       string               `json:"updatedAt"`
}

type CardPhoto struct {
	Big      string `json:"big"`
	C246x328 string `json:"c246x328"`
	C516x688 string `json:"c516x688"`
	Square   string `json:"square"`
	TM       string `json:"tm"`
}

type CardWholesale struct {
	Enabled bool `json:"enabled"`
	Quantum int  `json:"quantum"`
}

type CardDimensions struct {
	Length       float64 `json:"length"`
	Width        float64 `json:"width"`
	Height       float64 `json:"height"`
	WeightBrutto float64 `json:"weightBrutto"`
	IsValid      bool    `json:"isValid"`
}

type CardCharacteristic struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Value any    `json:"value"`
}

type CardSize struct {
	CHRTID   int64    `json:"chrtID"`
	TechSize string   `json:"techSize"`
	WBSize   string   `json:"wbSize"`
	SKUs     []string `json:"skus"`
}

type CardTag struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type TrashCardsListQuery struct {
	Locale Locale `url:"locale,omitempty"`
}

type TrashCardsListRequest struct {
	Settings TrashCardsListSettings `json:"settings"`
}

type TrashCardsListSettings struct {
	Sort   CardsSort             `json:"sort"`
	Filter *TrashCardsListFilter `json:"filter,omitempty"`
	Cursor TrashCardsListCursor  `json:"cursor"`
}

type TrashCardsListFilter struct {
	TextSearch string `json:"textSearch,omitempty"`
}

type TrashCardsListCursor struct {
	TrashedAt string `json:"trashedAt,omitempty"`
	NMID      int64  `json:"nmID,omitempty"`
	Limit     int    `json:"limit"`
}

type TrashCardsListResponse struct {
	Cards  []TrashCard              `json:"cards"`
	Cursor TrashCardsResponseCursor `json:"cursor"`
}

type TrashCardsResponseCursor struct {
	TrashedAt string `json:"trashedAt"`
	NMID      int64  `json:"nmID"`
	Total     int    `json:"total"`
}

type TrashCard struct {
	NMID        int64          `json:"nmID"`
	VendorCode  string         `json:"vendorCode"`
	KIZMarked   bool           `json:"kizMarked"`
	SubjectID   int64          `json:"subjectID"`
	SubjectName string         `json:"subjectName"`
	Photos      []CardPhoto    `json:"photos"`
	Video       string         `json:"video"`
	Wholesale   CardWholesale  `json:"wholesale"`
	Sizes       []CardSize     `json:"sizes"`
	Dimensions  CardDimensions `json:"dimensions"`
	CreatedAt   string         `json:"createdAt"`
	TrashedAt   string         `json:"trashedAt"`
}

type CardsErrorListQuery struct {
	Locale Locale `url:"locale,omitempty"`
}

type CardsErrorListRequest struct {
	Cursor CardsErrorListCursor `json:"cursor"`
	Order  CardsErrorListOrder  `json:"order"`
}

type CardsErrorListCursor struct {
	Limit     int    `json:"limit"`
	UpdatedAt string `json:"updatedAt,omitempty"`
	BatchUUID string `json:"batchUUID,omitempty"`
}

type CardsErrorListOrder struct {
	Ascending bool `json:"ascending"`
}

type CardsErrorListResponse struct {
	Data CardsErrorListData `json:"data"`
	ResponseMeta
}

type CardsErrorListData struct {
	Items  []CardsErrorBatch        `json:"items"`
	Cursor CardsErrorResponseCursor `json:"cursor"`
}

type CardsErrorResponseCursor struct {
	Next      bool   `json:"next"`
	UpdatedAt string `json:"updatedAt"`
	BatchUUID string `json:"batchUUID"`
}

type CardsErrorBatch struct {
	BatchUUID   string                       `json:"batchUUID"`
	Subjects    map[string]CardsErrorSubject `json:"subjects"`
	Brands      map[string]string            `json:"brands"`
	VendorCodes []string                     `json:"vendorCodes"`
	Errors      map[string][]string          `json:"errors"`
	UpdatedAt   string                       `json:"updatedAt"`
}

type CardsErrorSubject struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type UploadCardsRequest []UploadCardsGroup

type UploadCardsGroup struct {
	SubjectID int64        `json:"subjectID"`
	Variants  []UploadCard `json:"variants"`
}

type UploadCardsAddRequest struct {
	IMTID      int64        `json:"imtID"`
	CardsToAdd []UploadCard `json:"cardsToAdd"`
}

type UploadCard struct {
	VendorCode      string                 `json:"vendorCode"`
	KIZMarked       bool                   `json:"kizMarked,omitempty"`
	Wholesale       *UploadWholesale       `json:"wholesale,omitempty"`
	Title           string                 `json:"title,omitempty"`
	Description     string                 `json:"description,omitempty"`
	Brand           string                 `json:"brand,omitempty"`
	Dimensions      UploadDimensions       `json:"dimensions"`
	Characteristics []UploadCharacteristic `json:"characteristics,omitempty"`
	Sizes           []UploadSize           `json:"sizes"`
}

type UploadWholesale struct {
	Enabled bool `json:"enabled"`
	Quantum int  `json:"quantum"`
}

type UploadDimensions struct {
	Length       float64 `json:"length"`
	Width        float64 `json:"width"`
	Height       float64 `json:"height"`
	WeightBrutto float64 `json:"weightBrutto"`
}

type UploadCharacteristic struct {
	ID    int64 `json:"id"`
	Value any   `json:"value"`
}

type UploadSize struct {
	TechSize string   `json:"techSize,omitempty"`
	WBSize   string   `json:"wbSize,omitempty"`
	Price    *int64   `json:"price,omitempty"`
	SKUs     []string `json:"skus"`
}

type UploadCardsResponse struct {
	Data EmptyObject `json:"data"`
	ResponseMeta
}

type UploadCardsAddResponse struct {
	Data EmptyObject `json:"data"`
	ResponseMeta
}
