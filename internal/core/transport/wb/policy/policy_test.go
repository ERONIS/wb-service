package policy

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNewOperationEnforcesManifestInvariants(t *testing.T) {
	t.Parallel()

	valid := OperationSpec{
		ID:               "test.operation",
		Method:           http.MethodGet,
		Path:             "/test",
		BucketID:         "test_bucket",
		Kind:             OperationKindRead,
		RetryMode:        RetryModeReadSafe,
		SuccessStatuses:  []int{http.StatusOK},
		RequestMode:      BodyModeNone,
		ResponseMode:     BodyModeJSON,
		MaxRequestBytes:  0,
		MaxResponseBytes: 1024,
	}

	tests := []struct {
		name     string
		mutate   func(*OperationSpec)
		contains string
	}{
		{
			name: "mutation retry",
			mutate: func(spec *OperationSpec) {
				spec.Kind = OperationKindMutation
			},
			contains: "allows retry for non-read",
		},
		{
			name: "absolute path",
			mutate: func(spec *OperationSpec) {
				spec.Path = "https://example.com/test"
			},
			contains: "exactly one slash",
		},
		{
			name: "duplicate status",
			mutate: func(spec *OperationSpec) {
				spec.SuccessStatuses = []int{http.StatusOK, http.StatusOK}
			},
			contains: "duplicated",
		},
		{
			name: "missing response bound",
			mutate: func(spec *OperationSpec) {
				spec.MaxResponseBytes = 0
			},
			contains: "JSON response body",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			spec := valid
			spec.SuccessStatuses = append(
				[]int(nil),
				valid.SuccessStatuses...,
			)
			test.mutate(&spec)

			_, err := NewOperation(spec)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("NewOperation error = %v, want substring %q", err, test.contains)
			}
		})
	}
}

func TestOperationDoesNotExposeMutableStatusSlice(t *testing.T) {
	t.Parallel()

	statuses := []int{http.StatusOK}
	operation, err := NewOperation(OperationSpec{
		ID:               "test.operation",
		Method:           http.MethodGet,
		Path:             "/test",
		BucketID:         "test_bucket",
		Kind:             OperationKindRead,
		RetryMode:        RetryModeReadSafe,
		SuccessStatuses:  statuses,
		RequestMode:      BodyModeNone,
		ResponseMode:     BodyModeJSON,
		MaxResponseBytes: 1024,
	})
	if err != nil {
		t.Fatalf("create operation: %v", err)
	}

	statuses[0] = http.StatusCreated
	returned := operation.SuccessStatuses()
	returned[0] = http.StatusAccepted

	if !operation.IsSuccessStatus(http.StatusOK) {
		t.Fatal("operation status was mutated")
	}
	if operation.IsSuccessStatus(http.StatusCreated) ||
		operation.IsSuccessStatus(http.StatusAccepted) {
		t.Fatal("operation accepted mutated status")
	}
}

func TestBucketSpecValidate(t *testing.T) {
	t.Parallel()

	if err := (BucketSpec{
		ID:         "test",
		Interval:   time.Second,
		Burst:      1,
		MaxWaiters: 1,
	}).Validate(); err != nil {
		t.Fatalf("valid bucket rejected: %v", err)
	}

	if err := (BucketSpec{ID: "test"}).Validate(); err == nil {
		t.Fatal("invalid bucket unexpectedly accepted")
	}
}
