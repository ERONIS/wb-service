package v1

type SellerInfoResponse struct {
	Name      string `json:"name"`
	SellerID  string `json:"sid"`
	TradeMark string `json:"tradeMark"`
	TIN       string `json:"tin"`
}
