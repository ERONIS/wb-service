package cardpipeline

type Variant struct {
	VendorCode      string              `json:"vendorCode"`
	Title           string              `json:"title"`
	Description     string              `json:"description"`
	Brand           string              `json:"brand"`
	Dimensions      Dimensions          `json:"dimensions"`
	Sizes           []Size              `json:"sizes"`
	Characteristics []RawCharacteristic `json:"characteristics"`
}

type Dimensions struct {
	Length       float64 `json:"length"`
	Width        float64 `json:"width"`
	Height       float64 `json:"height"`
	WeightBrutto float64 `json:"weightBrutto"`
}

type Size struct {
	TechSize string   `json:"techSize"`
	WBSize   string   `json:"wbSize"`
	Price    int64    `json:"price,omitempty"`
	SKUs     []string `json:"skus"`
}

type RawCharacteristic struct {
	ID    int64  `json:"id,omitempty"`
	Name  string `json:"name"`
	Value any    `json:"value"`
}

type Media struct {
	Photos []string `json:"photos"`
	Video  string   `json:"video"`
}

// Card is the normalized immutable payload passed between card pipeline
// stages after source rows have been aggregated.
type Card struct {
	SourceRows []int  `json:"sourceRows"`
	Group      string `json:"group"`
	Category   string `json:"category"`
	Price      int64  `json:"price"`

	KIZRequirement   string `json:"kizRequirement"`
	AdultOnly        string `json:"adultOnly"`
	MarkingConfirmed string `json:"markingConfirmed"`
	VATRate          string `json:"vatRate"`

	Variant Variant `json:"variant"`
	Media   Media   `json:"media"`
}

func (card Card) Barcodes() []string {
	barcodes := make([]string, 0)
	for _, size := range card.Variant.Sizes {
		barcodes = append(barcodes, size.SKUs...)
	}
	return barcodes
}
