package store_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"notification-service/internal/store"
)

func TestValidateChannels_ValidInputs(t *testing.T) {
	cases := [][]string{
		{"websocket"},
		{"email"},
		{"sms"},
		{"websocket", "email", "sms"},
		{"email", "sms"},
	}
	for _, channels := range cases {
		assert.NoError(t, store.ValidateChannels(channels))
	}
}

func TestValidateChannels_InvalidInputs(t *testing.T) {
	cases := []struct {
		channels []string
		wantErr  bool
	}{
		{[]string{"push"}, true},
		{[]string{"EMAIL"}, true}, // case-sensitive
		{[]string{"websocket", "unknown"}, true},
		{[]string{""}, true},
	}
	for _, tc := range cases {
		err := store.ValidateChannels(tc.channels)
		if tc.wantErr {
			assert.Error(t, err)
		} else {
			assert.NoError(t, err)
		}
	}
}
