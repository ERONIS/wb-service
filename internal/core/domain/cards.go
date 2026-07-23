package domain

type Card struct {
	SubjectID int `json:"subjectID,omitempty"`

	Category string `json:"category,omitempty"`

	GroupID int `json:"groupId,omitempty"`

	Price int `json:"price"`

	Variant Variant `json:"variants"`

	Media *Media `json:"media,omitempty"`
}

type Variant struct {
	VendorCode  string     `json:"vendorCode"`
	Wholesale   Wholesale  `json:"wholesale"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Brand       string     `json:"brand"`
	Dimensions  Dimensions `json:"dimensions"`
	Sizes       []Size     `json:"sizes"`

	RawCharacteristics []RawCharacteristic `json:"rawCharacteristics,omitempty"`

	Characteristics []Characteristic `json:"characteristics,omitempty"`
}

type RawCharacteristic struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

type Characteristic struct {
	ID    int `json:"id"`
	Value any `json:"value"`
}

type Wholesale struct {
	Enabled bool `json:"enabled"`
	Quantum int  `json:"quantum"`
}

type Dimensions struct {
	Length       float64 `json:"length"`
	Width        float64 `json:"width"`
	Height       float64 `json:"height"`
	WeightBrutto float64 `json:"weightBrutto"`
}

type Size struct {
	TechSize string   `json:"techSize"`
	WbSize   string   `json:"wbSize"`
	Skus     []string `json:"skus"`
}

type Media struct {
	Photos []string `json:"photos"`
	Videos []string `json:"videos"`
}
