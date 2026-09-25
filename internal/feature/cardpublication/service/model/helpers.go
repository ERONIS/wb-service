package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ERONIS/wb-service/internal/core/canonicalhash"
)

// DecodeExactJSON decodes a canonical JSON payload and rejects alternate
// encodings. Publication journals rely on byte-for-byte stable requests.
func DecodeExactJSON(payload []byte, target any) error {
	if len(payload) == 0 || target == nil {
		return errors.New("exact JSON payload is invalid")
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return err
	}
	encoded, err := json.Marshal(target)
	if err != nil {
		return err
	}
	if !bytes.Equal(encoded, payload) {
		return errors.New("exact JSON payload is not canonical")
	}
	return nil
}

// DigestParts builds a domain-separated digest for journal evidence.
func DigestParts(domain string, parts ...[]byte) Digest {
	builder := canonicalhash.NewSHA256(domain)
	for _, part := range parts {
		builder.Bytes(part)
	}
	return Digest(builder.Sum())
}

// DigestMembers hashes the stable identity and order of action members.
func DigestMembers(members []ActionMemberDraft) Digest {
	parts := make([][]byte, 0, len(members)*4)
	for _, member := range members {
		parts = append(
			parts,
			[]byte(strconv.FormatInt(member.GroupTargetID, 10)),
			[]byte(strconv.FormatInt(member.TransferItemTargetID, 10)),
			[]byte(strconv.Itoa(member.RequestMemberIndex)),
			[]byte(member.VendorCode),
		)
	}
	return DigestParts("cardpublication-members:v1", parts...)
}

func NormalizeUniqueStrings(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func ParseWBTime(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}
