package wbcabinet_wb_transport

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_wb "github.com/ERONIS/wb-service/internal/core/transport/wb"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	client "github.com/ERONIS/wb-service/internal/core/transport/wb/client"
	wbcabinet_service "github.com/ERONIS/wb-service/internal/feature/wbcabinet/service"
)

type Runtime interface {
	NewCandidate(domain.SellerID, string, string) (*core_wb.CredentialCandidate, error)
	ActivateCandidate(*core_wb.CredentialCandidate, domain.ClientGeneration) error
	RemoveCabinet(domain.SellerID)
}

type Verifier struct {
	runtime Runtime
}

type preparedCredential struct {
	runtime   Runtime
	candidate *core_wb.CredentialCandidate
}

var _ wbcabinet_service.Verifier = (*Verifier)(nil)
var _ wbcabinet_service.PreparedCredential = (*preparedCredential)(nil)

func NewVerifier(runtime Runtime) *Verifier {
	if runtime == nil {
		panic("wbcabinet WB runtime is nil")
	}
	return &Verifier{runtime: runtime}
}

func (verifier *Verifier) Prepare(
	ctx context.Context,
	sellerID domain.SellerID,
	name string,
	token string,
) (wbcabinet_service.PreparedCredential, error) {
	if ctx == nil {
		return nil, errors.New("verify WB cabinet: context is nil")
	}
	candidate, err := verifier.runtime.NewCandidate(sellerID, name, token)
	if err != nil {
		return nil, fmt.Errorf("build WB credential candidate: %w", err)
	}
	ping, err := client.ExecuteResponse[contentapi.PingResponse](
		ctx,
		candidate.Executor(),
		contentapi.PingOperation(),
		nil,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("check WB Content API token: %w", classifyVerificationError(err))
	}
	if strings.TrimSpace(ping.Status) != "OK" {
		return nil, fmt.Errorf("WB Content API ping returned invalid status: %w", wbcabinet_service.ErrVerificationTransient)
	}
	if _, err := time.Parse(time.RFC3339, ping.Timestamp); err != nil {
		return nil, fmt.Errorf("WB Content API ping returned invalid timestamp: %w", wbcabinet_service.ErrVerificationTransient)
	}
	return &preparedCredential{
		runtime:   verifier.runtime,
		candidate: candidate,
	}, nil
}

func classifyVerificationError(err error) error {
	var classified client.ClassifiedError
	if errors.As(err, &classified) {
		switch status := classified.HTTPStatus(); {
		case status == http.StatusUnauthorized || status == http.StatusForbidden:
			return fmt.Errorf("%w: %w", wbcabinet_service.ErrVerification, err)
		case status == http.StatusTooManyRequests:
			return fmt.Errorf("%w: %w", wbcabinet_service.ErrVerificationRateLimited, err)
		case status >= http.StatusInternalServerError && status <= 599:
			return fmt.Errorf("%w: %w", wbcabinet_service.ErrVerificationTransient, err)
		}
		switch classified.Code() {
		case client.ErrorCodeTransport,
			client.ErrorCodeCanceled,
			client.ErrorCodeDeadlineExceeded,
			client.ErrorCodeRetryInterrupted,
			client.ErrorCodeEmptyResponse,
			client.ErrorCodeInvalidResponse:
			return fmt.Errorf("%w: %w", wbcabinet_service.ErrVerificationTransient, err)
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: %w", wbcabinet_service.ErrVerificationTransient, err)
	}
	return err
}

func (verifier *Verifier) Remove(sellerID domain.SellerID) {
	if verifier == nil || verifier.runtime == nil {
		return
	}
	verifier.runtime.RemoveCabinet(sellerID)
}

func (credential *preparedCredential) Activate(generation domain.ClientGeneration) error {
	if credential == nil || credential.runtime == nil || credential.candidate == nil {
		return errors.New("activate WB credential: prepared credential is invalid")
	}
	return credential.runtime.ActivateCandidate(credential.candidate, generation)
}
