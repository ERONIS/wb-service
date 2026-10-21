package v1

type DirectoryQuery = LocaleQuery

type DirectoryColorsResponse = DataResponse[[]DirectoryColor]

type DirectoryColor struct {
	Name       string `json:"name"`
	ParentName string `json:"parentName"`
}

type DirectoryKindsResponse = DataResponse[[]string]

type DirectoryCountriesResponse = DataResponse[[]DirectoryCountry]

type DirectoryCountry struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	FullName string `json:"fullName"`
}

type DirectorySeasonsResponse = DataResponse[[]string]

type DirectoryVATResponse = DataResponse[[]string]

type DirectoryTNVEDQuery struct {
	SubjectID int64  `url:"subjectID"`
	Search    int64  `url:"search,omitempty"`
	Locale    Locale `url:"locale,omitempty"`
}

type DirectoryTNVEDResponse = DataResponse[[]DirectoryTNVED]

type DirectoryTNVED struct {
	Code  string `json:"tnved"`
	IsKIZ bool   `json:"isKiz"`
}
