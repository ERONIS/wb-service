package cardimport_xlsx_transport

import "testing"

func TestParsePositiveInt64RoundsPrice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value string
		want  int64
	}{
		{value: "17520.0", want: 17520},
		{value: "20160,4", want: 20160},
		{value: "17940.5", want: 17941},
		{value: " 15 640.6 ", want: 15641},
	}
	for _, test := range tests {
		test := test
		t.Run(test.value, func(t *testing.T) {
			t.Parallel()
			got, err := parsePositiveInt64(test.value)
			if err != nil {
				t.Fatalf("parsePositiveInt64(%q) error = %v", test.value, err)
			}
			if got != test.want {
				t.Fatalf("parsePositiveInt64(%q) = %d, want %d", test.value, got, test.want)
			}
		})
	}
}

func TestParsePositiveInt64RejectsInvalidRoundedPrice(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", "0", "0.4", "-1", "NaN", "Inf", "text"} {
		if _, err := parsePositiveInt64(value); err == nil {
			t.Fatalf("parsePositiveInt64(%q) expected an error", value)
		}
	}
}
