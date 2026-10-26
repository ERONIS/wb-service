package domain

import "testing"

func TestClientGenerationValidationAndString(t *testing.T) {
	for _, generation := range []ClientGeneration{0, -1} {
		if !generation.IsZero() {
			t.Fatalf("поколение %d должно быть недействительным", generation)
		}
	}
	if generation := ClientGeneration(42); generation.IsZero() || generation.String() != "42" {
		t.Fatalf("неверное положительное поколение: %d (%q)", generation, generation.String())
	}
}
