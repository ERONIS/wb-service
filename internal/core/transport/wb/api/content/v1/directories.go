package v1

type DirectoryQuery struct {
	Locale Locale `url:"locale,omitempty"`
}

type DirectoryColorsResponse struct {
	Data []DirectoryColor `json:"data"`
	ResponseMeta
}

type DirectoryColor struct {
	Name       string `json:"name"`
	ParentName string `json:"parentName"`
}

type DirectoryKindsResponse struct {
	Data []string `json:"data"`
	ResponseMeta
}

type DirectoryCountriesResponse struct {
	Data []DirectoryCountry `json:"data"`
	ResponseMeta
}

type DirectoryCountry struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	FullName string `json:"fullName"`
}

type DirectorySeasonsResponse struct {
	Data []string `json:"data"`
	ResponseMeta
}

type DirectoryVATResponse struct {
	Data []string `json:"data"`
	ResponseMeta
}

type DirectoryTNVEDQuery struct {
	SubjectID int64  `url:"subjectID"`
	Search    int64  `url:"search,omitempty"`
	Locale    Locale `url:"locale,omitempty"`
}

type DirectoryTNVEDResponse struct {
	Data []DirectoryTNVED `json:"data"`
	ResponseMeta
}

type DirectoryTNVED struct {
	Code  string `json:"tnved"`
	IsKIZ bool   `json:"isKiz"`
}
