package main

import (
	"fmt"
	"testing"
)

func TestInstallerVersionsPreserveReleaseOrdering(t *testing.T) {
	tags := []string{"v1.2.2", "v1.2.3-alpha", "v1.2.3-alpha.0", "v1.2.3-alpha.29", "v1.2.3-beta", "v1.2.3-beta.29", "v1.2.3-rc", "v1.2.3-rc.29", "v1.2.3", "v1.2.4-alpha", "v1.3.0-alpha", "v2.0.0-alpha"}
	previous := 0
	for _, tag := range tags {
		version, err := installerVersion(tag)
		if err != nil {
			t.Fatal(err)
		}
		var major, minor, build int
		if _, err := fmt.Sscanf(version, "%d.%d.%d", &major, &minor, &build); err != nil {
			t.Fatal(err)
		}
		ordered := major<<24 | minor<<16 | build
		if ordered <= previous {
			t.Fatalf("%s (%s) does not upgrade the previous release", tag, version)
		}
		previous = ordered
	}
	if version, err := installerVersion("v255.255.654"); err != nil || version != "255.255.65499" {
		t.Fatalf("boundary: %q, %v", version, err)
	}
}

func TestInstallerVersionRejectsInvalidOrUnrepresentableTags(t *testing.T) {
	for _, tag := range []string{"v1.2.3.4", "v1.2.3-beta.30", "v1.2.3-dev.1", "v1.2.3-rc.-1", "v1.2.3-alpha.01", "v01.2.3", "v256.0.0", "v0.256.0", "v0.0.655", "v999999999999999999999.0.0", "v0.9.0.alpha.1", "v1.2.3\nMSI_VERSION=0"} {
		if _, err := installerVersion(tag); err == nil {
			t.Errorf("accepted invalid tag %q", tag)
		}
	}
}
