package wb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	client "github.com/ERONIS/wb-service/internal/core/transport/wb/client"
	config "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
)

const (
	contentCapabilityBit = uint64(1) << 1
	readOnlyBit          = uint64(1) << 30
)

type SellerKey [sha256.Size]byte
type ClientGeneration [sha256.Size]byte

type CredentialIdentity struct {
	CabinetID           config.CabinetID
	SellerKey           SellerKey
	ContentRead         bool
	ContentWrite        bool
	CredentialExpiresAt time.Time
	ClientGeneration    ClientGeneration
}

type tokenClaims struct {
	TokenID    string
	SellerID   string
	Properties uint64
	ExpiresAt  time.Time
}

// verifyCredentialsAtStartup сначала проверяет documented JWT claims, затем
// подтверждает каждый token безопасным read-запросом Content API.
func (clientset *Clientset) verifyCredentialsAtStartup(
	ctx context.Context,
	now time.Time,
) error {
	if ctx == nil {
		return errors.New("verify WB credentials: context is nil")
	}

	credentials, err := clientset.CredentialSnapshot(now)
	if err != nil {
		return err
	}

	for _, credential := range credentials {
		if !credential.ContentRead {
			return fmt.Errorf(
				"cabinet %q token has no Content capability",
				credential.CabinetID,
			)
		}

		executor, err := clientset.PinnedExecutor(
			credential.CabinetID,
			credential.ClientGeneration,
		)
		if err != nil {
			return fmt.Errorf(
				"pin cabinet %q executor: %w",
				credential.CabinetID,
				err,
			)
		}

		request := contentapi.CardsListRequest{
			Settings: contentapi.CardsListSettings{
				Sort:   contentapi.CardsSort{Ascending: true},
				Cursor: contentapi.CardsListCursor{Limit: 1},
			},
		}
		response, err := client.ExecuteResponse[contentapi.CardsListResponse](
			ctx,
			executor,
			contentapi.CardsListOperation(),
			nil,
			request,
		)
		if err != nil {
			return fmt.Errorf(
				"probe cabinet %q with Cards List: %w",
				credential.CabinetID,
				err,
			)
		}
		if response.Cursor.Total < 0 || len(response.Cards) > 1 {
			return fmt.Errorf(
				"Cards List probe for cabinet %q returned an invalid response",
				credential.CabinetID,
			)
		}
	}

	return nil
}

// CredentialSnapshot decodes documented JWT claims without executing any WB
// operation. It returns only safe digests and capability metadata.
func (clientset *Clientset) CredentialSnapshot(
	now time.Time,
) ([]CredentialIdentity, error) {
	if clientset == nil {
		return nil, errors.New("get WB credential snapshot: clientset is nil")
	}
	if now.IsZero() {
		return nil, errors.New("get WB credential snapshot: current time is empty")
	}

	targets := make([]CredentialIdentity, 0, len(clientset.cabinetInfos))
	for _, cabinet := range clientset.cabinetInfos {
		token, ok := clientset.credentialTokens[cabinet.ID]
		if !ok {
			return nil, fmt.Errorf(
				"get WB credential %q: credential is unavailable",
				cabinet.ID,
			)
		}
		claims, err := decodeCredentialToken(token, now.UTC())
		if err != nil {
			return nil, fmt.Errorf(
				"get WB credential %q: %w",
				cabinet.ID,
				err,
			)
		}
		contentRead := claims.Properties&contentCapabilityBit != 0
		contentWrite := contentRead && claims.Properties&readOnlyBit == 0
		targets = append(targets, CredentialIdentity{
			CabinetID:           cabinet.ID,
			SellerKey:           sellerKey(claims.SellerID),
			ContentRead:         contentRead,
			ContentWrite:        contentWrite,
			CredentialExpiresAt: claims.ExpiresAt,
			ClientGeneration:    clientGeneration(token),
		})
	}

	return targets, nil
}

func (clientset *Clientset) PinnedExecutor(
	id config.CabinetID,
	generation ClientGeneration,
) (client.Executor, error) {
	if clientset == nil {
		return nil, errors.New("pin WB executor: clientset is nil")
	}
	token, ok := clientset.credentialTokens[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrCabinetNotFound, id)
	}
	if clientGeneration(token) != generation {
		return nil, errors.New("pin WB executor: client generation changed")
	}
	return clientset.ForCabinet(id)
}

func decodeCredentialToken(token string, now time.Time) (tokenClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return tokenClaims{}, errors.New("token is not a compact JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return tokenClaims{}, errors.New("token payload is not base64url")
	}
	if len(payload) > 16*1024 {
		return tokenClaims{}, errors.New("token payload is too large")
	}

	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var values map[string]any
	if err := decoder.Decode(&values); err != nil {
		return tokenClaims{}, errors.New("token payload is not valid JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return tokenClaims{}, errors.New("token payload contains trailing JSON")
	}

	tokenID, err := requiredUUIDClaim(values, "id")
	if err != nil {
		return tokenClaims{}, err
	}
	sellerID, err := requiredUUIDClaim(values, "sid")
	if err != nil {
		return tokenClaims{}, err
	}
	properties, err := requiredUintClaim(values, "s")
	if err != nil {
		return tokenClaims{}, err
	}
	expiresUnix, err := requiredUintClaim(values, "exp")
	if err != nil || expiresUnix > uint64(^uint64(0)>>1) {
		return tokenClaims{}, errors.New("token claim exp is invalid")
	}
	expiresAt := time.Unix(int64(expiresUnix), 0).UTC()
	if !expiresAt.After(now.UTC()) {
		return tokenClaims{}, errors.New("token is expired")
	}

	return tokenClaims{
		TokenID:    tokenID,
		SellerID:   sellerID,
		Properties: properties,
		ExpiresAt:  expiresAt,
	}, nil
}

func requiredUUIDClaim(values map[string]any, name string) (string, error) {
	value, ok := values[name].(string)
	if !ok || !validCanonicalUUID(value) {
		return "", fmt.Errorf("token claim %s is not a canonical UUID", name)
	}
	return strings.ToLower(value), nil
}

func requiredUintClaim(values map[string]any, name string) (uint64, error) {
	number, ok := values[name].(json.Number)
	if !ok {
		return 0, fmt.Errorf("token claim %s is not an integer", name)
	}
	value, err := strconv.ParseUint(string(number), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("token claim %s is not an unsigned integer", name)
	}
	return value, nil
}

func validCanonicalUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, character := range value {
		switch index {
		case 8, 13, 18, 23:
			if character != '-' {
				return false
			}
		default:
			if !((character >= '0' && character <= '9') ||
				(character >= 'a' && character <= 'f') ||
				(character >= 'A' && character <= 'F')) {
				return false
			}
		}
	}
	return true
}

func sellerKey(sellerID string) SellerKey {
	return credentialDigest[SellerKey]("wb-seller-key:v1", sellerID)
}

func clientGeneration(token string) ClientGeneration {
	return credentialDigest[ClientGeneration]("wb-client-generation:v1", token)
}

func credentialDigest[T ~[sha256.Size]byte](domain, value string) T {
	hasher := sha256.New()
	writeDigestPart(hasher, domain)
	writeDigestPart(hasher, value)
	var result T
	copy(result[:], hasher.Sum(nil))
	return result
}

type digestWriter interface {
	Write([]byte) (int, error)
}

func writeDigestPart(writer digestWriter, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = writer.Write(length[:])
	_, _ = writer.Write([]byte(value))
}
