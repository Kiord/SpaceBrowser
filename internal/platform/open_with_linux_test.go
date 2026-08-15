//go:build linux

package platform

import "testing"

func TestLinuxOpenWithPortalResponses(t *testing.T) {
	for _, test := range []struct {
		name     string
		response uint32
		wantErr  bool
	}{
		{name: "accepted", response: 0},
		{name: "cancelled", response: 1},
		{name: "failed", response: 2, wantErr: true},
		{name: "unknown failure", response: 99, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := linuxOpenWithResponseError(test.response)
			if (err != nil) != test.wantErr {
				t.Fatalf("response %d error = %v, wantErr %v", test.response, err, test.wantErr)
			}
		})
	}
}
