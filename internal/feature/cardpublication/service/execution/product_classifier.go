package execution

import (
	"context"
	"errors"
	"fmt"
	"strings"

	core_wb "github.com/ERONIS/wb-service/internal/core/transport/wb"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

func executeProductMutation(
	ctx context.Context,
	transport CatalogTransport,
	action ProductAction,
) SubmissionResult {
	var meta contentapi.ResponseMeta
	var err error
	switch action.Kind {
	case ActionCreateGroup:
		var request contentapi.UploadCardsRequest
		if decodeErr := decodeExactJSON(action.RequestPayload, &request); decodeErr != nil {
			return classifySubmissionError(action, decodeErr)
		}
		var response contentapi.UploadCardsResponse
		response, err = transport.UploadCards(
			ctx,
			action.CabinetID,
			action.ClientGeneration,
			request,
		)
		meta = response.ResponseMeta
	case ActionAddToGroup:
		var request contentapi.UploadCardsAddRequest
		if decodeErr := decodeExactJSON(action.RequestPayload, &request); decodeErr != nil {
			return classifySubmissionError(action, decodeErr)
		}
		var response contentapi.UploadCardsAddResponse
		response, err = transport.UploadCardsAdd(
			ctx,
			action.CabinetID,
			action.ClientGeneration,
			request,
		)
		meta = response.ResponseMeta
	default:
		return internalNotDispatched(action, "UNSUPPORTED_PRODUCT_ACTION")
	}
	if err != nil {
		return classifySubmissionError(action, err)
	}
	return classifySubmissionEnvelope(action, meta)
}

func classifySubmissionEnvelope(
	action ProductAction,
	meta contentapi.ResponseMeta,
) SubmissionResult {
	if !meta.Error {
		return SubmissionResult{
			Delivery:          SubmissionResponseReceived,
			HTTPStatus:        200,
			ClassifierVersion: submissionClassifierVersion,
			Disposition:       SubmissionAccepted,
			SafeCode:          "WB_SUBMISSION_ACCEPTED",
		}
	}
	matched := mappedVendorCodes(meta.AdditionalErrors, action.VendorCodes())
	if len(matched) == len(action.Members) {
		members := make([]MemberSubmissionResult, len(action.Members))
		for index, member := range action.Members {
			members[index] = MemberSubmissionResult{
				ActionMemberID: member.ID,
				OutcomeClass:   transfer_service.ResultRejected,
				OutcomeCode:    "WB_ENVELOPE_MEMBER_REJECTED",
			}
		}
		return SubmissionResult{
			Delivery:          SubmissionResponseReceived,
			HTTPStatus:        200,
			ClassifierVersion: submissionClassifierVersion,
			Disposition:       SubmissionRejectedProven,
			SafeCode:          "WB_ENVELOPE_REJECTED",
			MemberResults:     members,
		}
	}
	return SubmissionResult{
		Delivery:          SubmissionResponseReceived,
		HTTPStatus:        200,
		ClassifierVersion: submissionClassifierVersion,
		Disposition:       SubmissionUncertain,
		SafeCode:          "WB_ENVELOPE_ERROR_UNMAPPED",
		UnmatchedCount:    len(action.Members) - len(matched),
	}
}

func classifySubmissionError(action ProductAction, err error) SubmissionResult {
	var classified core_wb.ClassifiedError
	if !errors.As(err, &classified) {
		return internalNotDispatched(action, "WB_CALL_NOT_DISPATCHED")
	}
	safeCode := "WB_TRANSPORT_" + strings.ToUpper(classified.Code())
	switch classified.Delivery() {
	case core_wb.NotDispatched:
		return internalNotDispatched(action, safeCode)
	case core_wb.ResponseReceived:
		return SubmissionResult{
			Delivery:          SubmissionResponseReceived,
			HTTPStatus:        classified.HTTPStatus(),
			ClassifierVersion: submissionClassifierVersion,
			Disposition:       SubmissionUncertain,
			SafeCode:          safeCode,
			UnmatchedCount:    len(action.Members),
		}
	case core_wb.UnknownDelivery:
		return SubmissionResult{
			Delivery:          SubmissionUnknownDelivery,
			ClassifierVersion: submissionClassifierVersion,
			Disposition:       SubmissionUncertain,
			SafeCode:          safeCode,
			UnmatchedCount:    len(action.Members),
		}
	default:
		return internalNotDispatched(action, "WB_DELIVERY_STATE_INVALID")
	}
}

func internalNotDispatched(action ProductAction, safeCode string) SubmissionResult {
	members := make([]MemberSubmissionResult, len(action.Members))
	for index, member := range action.Members {
		members[index] = MemberSubmissionResult{
			ActionMemberID: member.ID,
			OutcomeClass:   transfer_service.ResultInternalError,
			OutcomeCode:    safeCode,
		}
	}
	return SubmissionResult{
		Delivery:          SubmissionNotDispatched,
		ClassifierVersion: submissionClassifierVersion,
		Disposition:       SubmissionRejectedProven,
		SafeCode:          safeCode,
		MemberResults:     members,
	}
}

func mappedVendorCodes(value any, vendorCodes []string) map[string]struct{} {
	wanted := make(map[string]struct{}, len(vendorCodes))
	for _, vendorCode := range vendorCodes {
		wanted[vendorCode] = struct{}{}
	}
	matched := make(map[string]struct{})
	var walk func(any)
	walk = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			for key, nested := range typed {
				if _, exists := wanted[key]; exists {
					matched[key] = struct{}{}
				}
				walk(nested)
			}
		case []any:
			for _, nested := range typed {
				walk(nested)
			}
		case string:
			if _, exists := wanted[typed]; exists {
				matched[typed] = struct{}{}
			}
		}
	}
	walk(value)
	return matched
}

func ValidateSubmissionResult(result SubmissionResult) error {
	if result.ClassifierVersion <= 0 || result.SafeCode == "" ||
		len(result.SafeCode) > 128 || result.UnmatchedCount < 0 {
		return errors.New("submission result is invalid")
	}
	switch result.Delivery {
	case SubmissionNotDispatched:
		if result.HTTPStatus != 0 || result.Disposition != SubmissionRejectedProven {
			return errors.New("not-dispatched submission result is invalid")
		}
	case SubmissionResponseReceived:
		if result.HTTPStatus != 0 && (result.HTTPStatus < 100 || result.HTTPStatus > 599) {
			return errors.New("response status is invalid")
		}
	case SubmissionUnknownDelivery:
		if result.HTTPStatus != 0 || result.Disposition != SubmissionUncertain {
			return errors.New("unknown-delivery submission result is invalid")
		}
	default:
		return fmt.Errorf("submission delivery %q is invalid", result.Delivery)
	}
	switch result.Disposition {
	case SubmissionAccepted:
		if len(result.MemberResults) != 0 || result.UnmatchedCount != 0 {
			return errors.New("accepted submission has member failures")
		}
	case SubmissionUncertain:
		if len(result.MemberResults) != 0 || result.UnmatchedCount <= 0 {
			return errors.New("uncertain submission evidence is invalid")
		}
	case SubmissionRejectedProven:
		if len(result.MemberResults) == 0 || result.UnmatchedCount != 0 {
			return errors.New("proven rejection member evidence is invalid")
		}
		seen := make(map[int64]struct{}, len(result.MemberResults))
		for _, member := range result.MemberResults {
			if member.ActionMemberID <= 0 || member.OutcomeCode == "" ||
				len(member.OutcomeCode) > 128 ||
				(member.OutcomeClass != transfer_service.ResultRejected &&
					member.OutcomeClass != transfer_service.ResultInternalError) {
				return errors.New("proven rejection member result is invalid")
			}
			if _, exists := seen[member.ActionMemberID]; exists {
				return errors.New("proven rejection member is duplicated")
			}
			seen[member.ActionMemberID] = struct{}{}
		}
	default:
		return errors.New("submission disposition is invalid")
	}
	return nil
}
