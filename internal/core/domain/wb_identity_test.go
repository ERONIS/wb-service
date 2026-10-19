package domain

import "testing"

func TestSellerIDIsCanonicalUUID(t *testing.T) {
	valid := SellerID("a723f78d-4d8f-45eb-a40b-3d46841d1688")
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid seller ID rejected: %v", err)
	}
	for _, invalid := range []SellerID{
		"",
		"A723F78D-4D8F-45EB-A40B-3D46841D1688",
		"a723f78d4d8f45eba40b3d46841d1688",
		"a723f78d-4d8f-45eb-a40b-3d46841d168g",
	} {
		if err := invalid.Validate(); err == nil {
			t.Fatalf("invalid seller ID accepted: %q", invalid)
		}
	}
}

func TestClientGenerationChangesWithToken(t *testing.T) {
	first := NewClientGeneration("token-1")
	if first.IsZero() || first != NewClientGeneration("token-1") {
		t.Fatal("same token must have a stable generation")
	}
	if first == NewClientGeneration("token-2") {
		t.Fatal("different tokens must have different generations")
	}
}
