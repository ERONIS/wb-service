package v1

const (
	kibibyte int64 = 1024
	mebibyte       = 1024 * kibibyte
)

const (
	maxParentCategoriesResponseBytes       int64 = 4 * mebibyte
	maxSubjectsResponseBytes               int64 = 8 * mebibyte
	maxSubjectCharacteristicsResponseBytes int64 = 4 * mebibyte
	maxCardsLimitsResponseBytes            int64 = 1 * mebibyte
	maxBrandsResponseBytes                 int64 = 4 * mebibyte
	maxDirectoryResponseBytes              int64 = 4 * mebibyte
	maxCardsListRequestBytes               int64 = 64 * kibibyte
	maxCardsListResponseBytes              int64 = 32 * mebibyte
	maxTrashCardsListRequestBytes          int64 = 64 * kibibyte
	maxTrashCardsListResponseBytes         int64 = 32 * mebibyte
	maxCardsErrorListRequestBytes          int64 = 64 * kibibyte
	maxCardsErrorListResponseBytes         int64 = 32 * mebibyte
	maxUploadCardsRequestBytes             int64 = 10_000_000
	maxUploadCardsAddRequestBytes          int64 = 10_000_000
	maxMutationResponseBytes               int64 = 1 * mebibyte
	maxSaveMediaByLinksRequestBytes        int64 = 256 * kibibyte
)

const (
	MaxSubjectsPageSize        = 1000
	MaxCardsListPageSize       = 100
	MaxTrashCardsListPageSize  = 100
	MaxCardsErrorListPageSize  = 100
	MaxUploadGroups            = 100
	MaxVariantsPerGroup        = 30
	MaxUploadAddVariants       = 29
	MaxMediaImages             = 30
	MaxMediaVideos             = 1
	MaxMediaLinks              = MaxMediaImages + MaxMediaVideos
	MaxProductTitleRunes       = 60
	MaxProductDescriptionRunes = 5000
)
