package v2

type GoodsByNMRequest struct {
	NMList []int64 `json:"nmList"`
}

type GoodsResponse struct {
	Data      GoodsData `json:"data"`
	Error     bool      `json:"error"`
	ErrorText string    `json:"errorText"`
}

type GoodsData struct {
	ListGoods []Good `json:"listGoods"`
}

type Good struct {
	NMID              int64      `json:"nmID"`
	VendorCode        string     `json:"vendorCode"`
	Sizes             []GoodSize `json:"sizes"`
	Currency          string     `json:"currencyIsoCode4217"`
	Discount          int        `json:"discount"`
	ClubDiscount      int        `json:"clubDiscount"`
	EditableSizePrice bool       `json:"editableSizePrice"`
}

type GoodSize struct {
	SizeID              int64   `json:"sizeID"`
	Price               int64   `json:"price"`
	DiscountedPrice     float64 `json:"discountedPrice"`
	ClubDiscountedPrice float64 `json:"clubDiscountedPrice"`
	TechSizeName        string  `json:"techSizeName"`
}
