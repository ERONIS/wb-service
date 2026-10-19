package wbcabinet_wb_transport

import (
	"context"
	"errors"
	"fmt"
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
	ActivateCandidate(*core_wb.CredentialCandidate) error
	RemoveCabinet(domain.SellerID)
}

type Verifier struct {
	runtime Runtime
}

func NewVerifier(runtime Runtime) *Verifier {
	if runtime == nil {
		panic("wbcabinet WB runtime is nil")
	}
	return &Verifier{runtime: runtime}
}

func (verifier *Verifier) Prepare(
	ctx context.Context,
	sellerID wbcabinet_service.SellerID,
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
		return nil, errors.New("WB Content API ping returned invalid status")
	}
	if _, err := time.Parse(time.RFC3339, ping.Timestamp); err != nil {
		return nil, errors.New("WB Content API ping returned invalid timestamp")
	}
	return &preparedCredential{
		runtime:   verifier.runtime,
		candidate: candidate,
	}, nil
}

func classifyVerificationError(err error) error {
	var classified client.ClassifiedError
	if errors.As(err, &classified) {
		switch classified.Code() {
		case client.ErrorCodeRateLimited:
			return errors.Join(wbcabinet_service.ErrVerificationRateLimited, err)
		case client.ErrorCodeTransport, client.ErrorCodeCanceled, client.ErrorCodeDeadlineExceeded, client.ErrorCodeRetryInterrupted:
			return errors.Join(wbcabinet_service.ErrVerificationTransient, err)
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return errors.Join(wbcabinet_service.ErrVerificationTransient, err)
	}
	return err
}

func (verifier *Verifier) Remove(sellerID wbcabinet_service.SellerID) {
	if verifier == nil || verifier.runtime == nil {
		return
	}
	verifier.runtime.RemoveCabinet(sellerID)
}

type preparedCredential struct {
	runtime   Runtime
	candidate *core_wb.CredentialCandidate
}

func (credential *preparedCredential) Generation() wbcabinet_service.ClientGeneration {
	if credential == nil || credential.candidate == nil {
		return wbcabinet_service.ClientGeneration{}
	}
	return credential.candidate.Generation()
}

func (credential *preparedCredential) Activate() error {
	if credential == nil || credential.runtime == nil || credential.candidate == nil {
		return errors.New("activate WB credential: prepared credential is invalid")
	}
	return credential.runtime.ActivateCandidate(credential.candidate)
}

var _ wbcabinet_service.Verifier = (*Verifier)(nil)
var _ wbcabinet_service.PreparedCredential = (*preparedCredential)(nil)
