package statistics_telegram_transport

import "testing"

func TestCallbackArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		arguments []string
		wantLen   int
	}{
		{name: "nil", arguments: nil, wantLen: 0},
		{name: "telebot empty callback", arguments: []string{""}, wantLen: 0},
		{name: "batch ID", arguments: []string{"2s"}, wantLen: 1},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := len(callbackArguments(test.arguments)); got != test.wantLen {
				t.Fatalf("callbackArguments() length = %d, want %d", got, test.wantLen)
			}
		})
	}
}
