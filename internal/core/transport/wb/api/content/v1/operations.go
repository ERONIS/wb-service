package v1

import (
	"fmt"
	"net/http"
	"strconv"

	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

const (
	contentAPIHost = "content-api.wildberries.ru"

	operationIDPing                   policy.OperationID = "content.ping"
	operationIDParentCategories       policy.OperationID = "content.object.parent-all"
	operationIDSubjects               policy.OperationID = "content.object.all"
	operationIDSubjectCharacteristics policy.OperationID = "content.object.characteristics"
	operationIDCardsLimits            policy.OperationID = "content.cards.limits"
	operationIDTags                   policy.OperationID = "content.tags.list"
	operationIDBrands                 policy.OperationID = "content.brands"
	operationIDDirectoryColors        policy.OperationID = "content.directory.colors"
	operationIDDirectoryKinds         policy.OperationID = "content.directory.kinds"
	operationIDDirectoryCountries     policy.OperationID = "content.directory.countries"
	operationIDDirectorySeasons       policy.OperationID = "content.directory.seasons"
	operationIDDirectoryVAT           policy.OperationID = "content.directory.vat"
	operationIDDirectoryTNVED         policy.OperationID = "content.directory.tnved"
	operationIDCardsList              policy.OperationID = "content.cards.list"
	operationIDTrashCardsList         policy.OperationID = "content.cards.trash-list"
	operationIDCardsErrorList         policy.OperationID = "content.cards.error-list"
	operationIDUploadCards            policy.OperationID = "content.cards.upload"
	operationIDUploadCardsAdd         policy.OperationID = "content.cards.upload-add"
	operationIDUpdateCards            policy.OperationID = "content.cards.update"
	operationIDSaveMediaByLinks       policy.OperationID = "content.media.save"
	operationIDUploadMediaFile        policy.OperationID = "content.media.file"
)

const (
	bucketIDContentPing          policy.BucketID = "content_ping"
	bucketIDContentCommon        policy.BucketID = "content_common"
	bucketIDBrands               policy.BucketID = "content_brands"
	bucketIDCharacteristics      policy.BucketID = "content_characteristics"
	bucketIDCardsLimitsAndErrors policy.BucketID = "content_cards_limits_and_errors"
	bucketIDCardsList            policy.BucketID = "content_cards_list"
	bucketIDCardsUpload          policy.BucketID = "content_cards_upload"
	bucketIDCardsUploadAdd       policy.BucketID = "content_cards_upload_add"
	bucketIDCardsUpdate          policy.BucketID = "content_cards_update"
	bucketIDMediaFiles           policy.BucketID = "content_media_files"
)

var (
	pingOperation = mustOperation(policy.OperationSpec{
		ID:               operationIDPing,
		Method:           http.MethodGet,
		Host:             contentAPIHost,
		Path:             "/ping",
		BucketID:         bucketIDContentPing,
		Kind:             policy.OperationKindRead,
		RetryMode:        policy.RetryModeNever,
		SuccessStatuses:  []int{http.StatusOK},
		RequestMode:      policy.BodyModeNone,
		ResponseMode:     policy.BodyModeJSON,
		MaxRequestBytes:  0,
		MaxResponseBytes: 16 * 1024,
	})

	parentCategoriesOperation = mustOperation(safeGetJSON(
		operationIDParentCategories, "/content/v2/object/parent/all",
		bucketIDContentCommon, maxParentCategoriesResponseBytes,
	))
	subjectsOperation = mustOperation(safeGetJSON(
		operationIDSubjects, "/content/v2/object/all",
		bucketIDContentCommon, maxSubjectsResponseBytes,
	))
	cardsLimitsOperation = mustOperation(safeGetJSON(
		operationIDCardsLimits, "/content/v2/cards/limits",
		bucketIDCardsLimitsAndErrors, maxCardsLimitsResponseBytes,
	))
	tagsOperation = mustOperation(safeGetJSON(
		operationIDTags, "/content/v2/tags",
		bucketIDContentCommon, maxTagsResponseBytes,
	))
	brandsOperation = mustOperation(safeGetJSON(
		operationIDBrands, "/api/content/v1/brands",
		bucketIDBrands, maxBrandsResponseBytes,
	))
	directoryColorsOperation = mustOperation(safeGetJSON(
		operationIDDirectoryColors, "/content/v2/directory/colors",
		bucketIDCharacteristics, maxDirectoryResponseBytes,
	))
	directoryKindsOperation = mustOperation(safeGetJSON(
		operationIDDirectoryKinds, "/content/v2/directory/kinds",
		bucketIDCharacteristics, maxDirectoryResponseBytes,
	))
	directoryCountriesOperation = mustOperation(safeGetJSON(
		operationIDDirectoryCountries, "/content/v2/directory/countries",
		bucketIDCharacteristics, maxDirectoryResponseBytes,
	))
	directorySeasonsOperation = mustOperation(safeGetJSON(
		operationIDDirectorySeasons, "/content/v2/directory/seasons",
		bucketIDCharacteristics, maxDirectoryResponseBytes,
	))
	directoryVATOperation = mustOperation(safeGetJSON(
		operationIDDirectoryVAT, "/content/v2/directory/vat",
		bucketIDCharacteristics, maxDirectoryResponseBytes,
	))
	directoryTNVEDOperation = mustOperation(safeGetJSON(
		operationIDDirectoryTNVED, "/content/v2/directory/tnved",
		bucketIDCharacteristics, maxDirectoryResponseBytes,
	))
	cardsListOperation = mustOperation(safePostJSON(
		operationIDCardsList, "/content/v2/get/cards/list",
		bucketIDCardsList, maxCardsListRequestBytes, maxCardsListResponseBytes,
	))
	trashCardsListOperation = mustOperation(safePostJSON(
		operationIDTrashCardsList, "/content/v2/get/cards/trash",
		bucketIDContentCommon, maxTrashCardsListRequestBytes, maxTrashCardsListResponseBytes,
	))
	cardsErrorListOperation = mustOperation(safePostJSON(
		operationIDCardsErrorList, "/content/v2/cards/error/list",
		bucketIDCardsLimitsAndErrors, maxCardsErrorListRequestBytes, maxCardsErrorListResponseBytes,
	))
	uploadCardsOperation = mustOperation(mutationPostJSON(
		operationIDUploadCards, "/content/v2/cards/upload",
		bucketIDCardsUpload, maxUploadCardsRequestBytes,
	))
	uploadCardsAddOperation = mustOperation(mutationPostJSON(
		operationIDUploadCardsAdd, "/content/v2/cards/upload/add",
		bucketIDCardsUploadAdd, maxUploadCardsAddRequestBytes,
	))
	updateCardsOperation = mustOperation(mutationPostJSON(
		operationIDUpdateCards, "/content/v2/cards/update",
		bucketIDCardsUpdate, maxUpdateCardsRequestBytes,
	))
	saveMediaByLinksOperation = mustOperation(mutationPostJSON(
		operationIDSaveMediaByLinks, "/content/v3/media/save",
		bucketIDMediaFiles, maxSaveMediaByLinksRequestBytes,
	))
)

func safeGetJSON(
	id policy.OperationID,
	path string,
	bucket policy.BucketID,
	maxResponseBytes int64,
) policy.OperationSpec {
	return policy.OperationSpec{
		ID:               id,
		Method:           http.MethodGet,
		Host:             contentAPIHost,
		Path:             path,
		BucketID:         bucket,
		Kind:             policy.OperationKindRead,
		RetryMode:        policy.RetryModeReadSafe,
		SuccessStatuses:  []int{http.StatusOK},
		RequestMode:      policy.BodyModeNone,
		ResponseMode:     policy.BodyModeJSON,
		MaxResponseBytes: maxResponseBytes,
	}
}

func safePostJSON(
	id policy.OperationID,
	path string,
	bucket policy.BucketID,
	maxRequestBytes, maxResponseBytes int64,
) policy.OperationSpec {
	spec := safeGetJSON(id, path, bucket, maxResponseBytes)
	spec.Method = http.MethodPost
	spec.RequestMode = policy.BodyModeJSON
	spec.MaxRequestBytes = maxRequestBytes
	return spec
}

func mutationPostJSON(
	id policy.OperationID,
	path string,
	bucket policy.BucketID,
	maxRequestBytes int64,
) policy.OperationSpec {
	return policy.OperationSpec{
		ID:               id,
		Method:           http.MethodPost,
		Host:             contentAPIHost,
		Path:             path,
		BucketID:         bucket,
		Kind:             policy.OperationKindMutation,
		RetryMode:        policy.RetryModeNever,
		SuccessStatuses:  []int{http.StatusOK},
		RequestMode:      policy.BodyModeJSON,
		ResponseMode:     policy.BodyModeJSON,
		MaxRequestBytes:  maxRequestBytes,
		MaxResponseBytes: maxMutationResponseBytes,
	}
}

func PingOperation() policy.Operation {
	return pingOperation
}

func ParentCategoriesOperation() policy.Operation {
	return parentCategoriesOperation
}

func SubjectsOperation() policy.Operation {
	return subjectsOperation
}

func SubjectCharacteristicsOperation(subjectID int64) (policy.Operation, error) {
	if subjectID <= 0 {
		return policy.Operation{}, fmt.Errorf(
			"subject ID must be positive: %d",
			subjectID,
		)
	}
	return policy.NewOperation(safeGetJSON(
		operationIDSubjectCharacteristics,
		"/content/v2/object/charcs/"+strconv.FormatInt(subjectID, 10),
		bucketIDContentCommon,
		maxSubjectCharacteristicsResponseBytes,
	))
}

func CardsLimitsOperation() policy.Operation {
	return cardsLimitsOperation
}

func TagsOperation() policy.Operation {
	return tagsOperation
}

func BrandsOperation() policy.Operation {
	return brandsOperation
}

func DirectoryColorsOperation() policy.Operation {
	return directoryColorsOperation
}

func DirectoryKindsOperation() policy.Operation {
	return directoryKindsOperation
}

func DirectoryCountriesOperation() policy.Operation {
	return directoryCountriesOperation
}

func DirectorySeasonsOperation() policy.Operation {
	return directorySeasonsOperation
}

func DirectoryVATOperation() policy.Operation {
	return directoryVATOperation
}

func DirectoryTNVEDOperation() policy.Operation {
	return directoryTNVEDOperation
}

func CardsListOperation() policy.Operation {
	return cardsListOperation
}

func TrashCardsListOperation() policy.Operation {
	return trashCardsListOperation
}

func CardsErrorListOperation() policy.Operation {
	return cardsErrorListOperation
}

func UploadCardsOperation() policy.Operation {
	return uploadCardsOperation
}

func UploadCardsAddOperation() policy.Operation {
	return uploadCardsAddOperation
}

func UpdateCardsOperation() policy.Operation {
	return updateCardsOperation
}

func SaveMediaByLinksOperation() policy.Operation {
	return saveMediaByLinksOperation
}

func UploadMediaFileOperation(nmID int64, mediaNumber int) (policy.Operation, error) {
	if nmID <= 0 {
		return policy.Operation{}, fmt.Errorf("media nmID must be positive: %d", nmID)
	}
	if mediaNumber <= 0 || mediaNumber > MaxMediaImages {
		return policy.Operation{}, fmt.Errorf("media number must be between 1 and %d: %d", MaxMediaImages, mediaNumber)
	}
	return policy.NewOperation(policy.OperationSpec{
		ID:               operationIDUploadMediaFile,
		Method:           http.MethodPost,
		Host:             contentAPIHost,
		Path:             "/content/v3/media/file",
		BucketID:         bucketIDMediaFiles,
		Kind:             policy.OperationKindMutation,
		RetryMode:        policy.RetryModeNever,
		SuccessStatuses:  []int{http.StatusOK},
		RequestMode:      policy.BodyModeMultipart,
		ResponseMode:     policy.BodyModeJSON,
		MaxRequestBytes:  maxUploadMediaFileRequestBytes,
		MaxResponseBytes: maxMutationResponseBytes,
		Headers: http.Header{
			"X-Nm-Id":        []string{strconv.FormatInt(nmID, 10)},
			"X-Photo-Number": []string{strconv.Itoa(mediaNumber)},
		},
	})
}

func mustOperation(spec policy.OperationSpec) policy.Operation {
	operation, err := policy.NewOperation(spec)
	if err != nil {
		panic(err)
	}

	return operation
}
