package v1

import (
	"fmt"
	"net/http"
	"strconv"

	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

const (
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

	parentCategoriesOperation = mustOperation(policy.OperationSpec{
		ID:               operationIDParentCategories,
		Method:           http.MethodGet,
		Path:             "/content/v2/object/parent/all",
		BucketID:         bucketIDContentCommon,
		Kind:             policy.OperationKindRead,
		RetryMode:        policy.RetryModeReadSafe,
		SuccessStatuses:  []int{http.StatusOK},
		RequestMode:      policy.BodyModeNone,
		ResponseMode:     policy.BodyModeJSON,
		MaxRequestBytes:  0,
		MaxResponseBytes: maxParentCategoriesResponseBytes,
	})

	subjectsOperation = mustOperation(policy.OperationSpec{
		ID:               operationIDSubjects,
		Method:           http.MethodGet,
		Path:             "/content/v2/object/all",
		BucketID:         bucketIDContentCommon,
		Kind:             policy.OperationKindRead,
		RetryMode:        policy.RetryModeReadSafe,
		SuccessStatuses:  []int{http.StatusOK},
		RequestMode:      policy.BodyModeNone,
		ResponseMode:     policy.BodyModeJSON,
		MaxRequestBytes:  0,
		MaxResponseBytes: maxSubjectsResponseBytes,
	})

	cardsLimitsOperation = mustOperation(policy.OperationSpec{
		ID:               operationIDCardsLimits,
		Method:           http.MethodGet,
		Path:             "/content/v2/cards/limits",
		BucketID:         bucketIDCardsLimitsAndErrors,
		Kind:             policy.OperationKindRead,
		RetryMode:        policy.RetryModeReadSafe,
		SuccessStatuses:  []int{http.StatusOK},
		RequestMode:      policy.BodyModeNone,
		ResponseMode:     policy.BodyModeJSON,
		MaxRequestBytes:  0,
		MaxResponseBytes: maxCardsLimitsResponseBytes,
	})

	tagsOperation = mustOperation(policy.OperationSpec{
		ID:               operationIDTags,
		Method:           http.MethodGet,
		Path:             "/content/v2/tags",
		BucketID:         bucketIDContentCommon,
		Kind:             policy.OperationKindRead,
		RetryMode:        policy.RetryModeReadSafe,
		SuccessStatuses:  []int{http.StatusOK},
		RequestMode:      policy.BodyModeNone,
		ResponseMode:     policy.BodyModeJSON,
		MaxRequestBytes:  0,
		MaxResponseBytes: maxTagsResponseBytes,
	})

	brandsOperation = mustOperation(policy.OperationSpec{
		ID:               operationIDBrands,
		Method:           http.MethodGet,
		Path:             "/api/content/v1/brands",
		BucketID:         bucketIDBrands,
		Kind:             policy.OperationKindRead,
		RetryMode:        policy.RetryModeReadSafe,
		SuccessStatuses:  []int{http.StatusOK},
		RequestMode:      policy.BodyModeNone,
		ResponseMode:     policy.BodyModeJSON,
		MaxRequestBytes:  0,
		MaxResponseBytes: maxBrandsResponseBytes,
	})

	directoryColorsOperation = mustOperation(policy.OperationSpec{
		ID:               operationIDDirectoryColors,
		Method:           http.MethodGet,
		Path:             "/content/v2/directory/colors",
		BucketID:         bucketIDCharacteristics,
		Kind:             policy.OperationKindRead,
		RetryMode:        policy.RetryModeReadSafe,
		SuccessStatuses:  []int{http.StatusOK},
		RequestMode:      policy.BodyModeNone,
		ResponseMode:     policy.BodyModeJSON,
		MaxRequestBytes:  0,
		MaxResponseBytes: maxDirectoryResponseBytes,
	})

	directoryKindsOperation = mustOperation(policy.OperationSpec{
		ID:               operationIDDirectoryKinds,
		Method:           http.MethodGet,
		Path:             "/content/v2/directory/kinds",
		BucketID:         bucketIDCharacteristics,
		Kind:             policy.OperationKindRead,
		RetryMode:        policy.RetryModeReadSafe,
		SuccessStatuses:  []int{http.StatusOK},
		RequestMode:      policy.BodyModeNone,
		ResponseMode:     policy.BodyModeJSON,
		MaxRequestBytes:  0,
		MaxResponseBytes: maxDirectoryResponseBytes,
	})

	directoryCountriesOperation = mustOperation(policy.OperationSpec{
		ID:               operationIDDirectoryCountries,
		Method:           http.MethodGet,
		Path:             "/content/v2/directory/countries",
		BucketID:         bucketIDCharacteristics,
		Kind:             policy.OperationKindRead,
		RetryMode:        policy.RetryModeReadSafe,
		SuccessStatuses:  []int{http.StatusOK},
		RequestMode:      policy.BodyModeNone,
		ResponseMode:     policy.BodyModeJSON,
		MaxRequestBytes:  0,
		MaxResponseBytes: maxDirectoryResponseBytes,
	})

	directorySeasonsOperation = mustOperation(policy.OperationSpec{
		ID:               operationIDDirectorySeasons,
		Method:           http.MethodGet,
		Path:             "/content/v2/directory/seasons",
		BucketID:         bucketIDCharacteristics,
		Kind:             policy.OperationKindRead,
		RetryMode:        policy.RetryModeReadSafe,
		SuccessStatuses:  []int{http.StatusOK},
		RequestMode:      policy.BodyModeNone,
		ResponseMode:     policy.BodyModeJSON,
		MaxRequestBytes:  0,
		MaxResponseBytes: maxDirectoryResponseBytes,
	})

	directoryVATOperation = mustOperation(policy.OperationSpec{
		ID:               operationIDDirectoryVAT,
		Method:           http.MethodGet,
		Path:             "/content/v2/directory/vat",
		BucketID:         bucketIDCharacteristics,
		Kind:             policy.OperationKindRead,
		RetryMode:        policy.RetryModeReadSafe,
		SuccessStatuses:  []int{http.StatusOK},
		RequestMode:      policy.BodyModeNone,
		ResponseMode:     policy.BodyModeJSON,
		MaxRequestBytes:  0,
		MaxResponseBytes: maxDirectoryResponseBytes,
	})

	directoryTNVEDOperation = mustOperation(policy.OperationSpec{
		ID:               operationIDDirectoryTNVED,
		Method:           http.MethodGet,
		Path:             "/content/v2/directory/tnved",
		BucketID:         bucketIDCharacteristics,
		Kind:             policy.OperationKindRead,
		RetryMode:        policy.RetryModeReadSafe,
		SuccessStatuses:  []int{http.StatusOK},
		RequestMode:      policy.BodyModeNone,
		ResponseMode:     policy.BodyModeJSON,
		MaxRequestBytes:  0,
		MaxResponseBytes: maxDirectoryResponseBytes,
	})

	cardsListOperation = mustOperation(policy.OperationSpec{
		ID:               operationIDCardsList,
		Method:           http.MethodPost,
		Path:             "/content/v2/get/cards/list",
		BucketID:         bucketIDCardsList,
		Kind:             policy.OperationKindRead,
		RetryMode:        policy.RetryModeReadSafe,
		SuccessStatuses:  []int{http.StatusOK},
		RequestMode:      policy.BodyModeJSON,
		ResponseMode:     policy.BodyModeJSON,
		MaxRequestBytes:  maxCardsListRequestBytes,
		MaxResponseBytes: maxCardsListResponseBytes,
	})

	trashCardsListOperation = mustOperation(policy.OperationSpec{
		ID:               operationIDTrashCardsList,
		Method:           http.MethodPost,
		Path:             "/content/v2/get/cards/trash",
		BucketID:         bucketIDContentCommon,
		Kind:             policy.OperationKindRead,
		RetryMode:        policy.RetryModeReadSafe,
		SuccessStatuses:  []int{http.StatusOK},
		RequestMode:      policy.BodyModeJSON,
		ResponseMode:     policy.BodyModeJSON,
		MaxRequestBytes:  maxTrashCardsListRequestBytes,
		MaxResponseBytes: maxTrashCardsListResponseBytes,
	})

	cardsErrorListOperation = mustOperation(policy.OperationSpec{
		ID:               operationIDCardsErrorList,
		Method:           http.MethodPost,
		Path:             "/content/v2/cards/error/list",
		BucketID:         bucketIDCardsLimitsAndErrors,
		Kind:             policy.OperationKindRead,
		RetryMode:        policy.RetryModeReadSafe,
		SuccessStatuses:  []int{http.StatusOK},
		RequestMode:      policy.BodyModeJSON,
		ResponseMode:     policy.BodyModeJSON,
		MaxRequestBytes:  maxCardsErrorListRequestBytes,
		MaxResponseBytes: maxCardsErrorListResponseBytes,
	})

	uploadCardsOperation = mustOperation(policy.OperationSpec{
		ID:               operationIDUploadCards,
		Method:           http.MethodPost,
		Path:             "/content/v2/cards/upload",
		BucketID:         bucketIDCardsUpload,
		Kind:             policy.OperationKindMutation,
		RetryMode:        policy.RetryModeNever,
		SuccessStatuses:  []int{http.StatusOK},
		RequestMode:      policy.BodyModeJSON,
		ResponseMode:     policy.BodyModeJSON,
		MaxRequestBytes:  maxUploadCardsRequestBytes,
		MaxResponseBytes: maxMutationResponseBytes,
	})

	uploadCardsAddOperation = mustOperation(policy.OperationSpec{
		ID:               operationIDUploadCardsAdd,
		Method:           http.MethodPost,
		Path:             "/content/v2/cards/upload/add",
		BucketID:         bucketIDCardsUploadAdd,
		Kind:             policy.OperationKindMutation,
		RetryMode:        policy.RetryModeNever,
		SuccessStatuses:  []int{http.StatusOK},
		RequestMode:      policy.BodyModeJSON,
		ResponseMode:     policy.BodyModeJSON,
		MaxRequestBytes:  maxUploadCardsAddRequestBytes,
		MaxResponseBytes: maxMutationResponseBytes,
	})

	updateCardsOperation = mustOperation(policy.OperationSpec{
		ID:               operationIDUpdateCards,
		Method:           http.MethodPost,
		Path:             "/content/v2/cards/update",
		BucketID:         bucketIDCardsUpdate,
		Kind:             policy.OperationKindMutation,
		RetryMode:        policy.RetryModeNever,
		SuccessStatuses:  []int{http.StatusOK},
		RequestMode:      policy.BodyModeJSON,
		ResponseMode:     policy.BodyModeJSON,
		MaxRequestBytes:  maxUpdateCardsRequestBytes,
		MaxResponseBytes: maxMutationResponseBytes,
	})

	saveMediaByLinksOperation = mustOperation(policy.OperationSpec{
		ID:               operationIDSaveMediaByLinks,
		Method:           http.MethodPost,
		Path:             "/content/v3/media/save",
		BucketID:         bucketIDMediaFiles,
		Kind:             policy.OperationKindMutation,
		RetryMode:        policy.RetryModeNever,
		SuccessStatuses:  []int{http.StatusOK},
		RequestMode:      policy.BodyModeJSON,
		ResponseMode:     policy.BodyModeJSON,
		MaxRequestBytes:  maxSaveMediaByLinksRequestBytes,
		MaxResponseBytes: maxMutationResponseBytes,
	})
)

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

	return policy.NewOperation(policy.OperationSpec{
		ID:     operationIDSubjectCharacteristics,
		Method: http.MethodGet,
		Path: "/content/v2/object/charcs/" + strconv.FormatInt(
			subjectID,
			10,
		),
		BucketID:         bucketIDContentCommon,
		Kind:             policy.OperationKindRead,
		RetryMode:        policy.RetryModeReadSafe,
		SuccessStatuses:  []int{http.StatusOK},
		RequestMode:      policy.BodyModeNone,
		ResponseMode:     policy.BodyModeJSON,
		MaxRequestBytes:  0,
		MaxResponseBytes: maxSubjectCharacteristicsResponseBytes,
	})
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
