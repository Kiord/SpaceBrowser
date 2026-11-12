//go:build windows

package platform

import "testing"

func TestUnknownAssociationDoesNotReturnTheApplicationChooser(t *testing.T) {
	const path = `C:\example.spacebrowser-unregistered-test-extension-6208`
	if got, err := (Windows{}).DefaultApplicationPath(path); err == nil {
		t.Fatalf("unknown association returned application %q", got)
	}
	if got, err := (Windows{}).DefaultApplicationName(path); err == nil {
		t.Fatalf("unknown association returned application name %q", got)
	}
}
