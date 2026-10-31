package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
)

var releaseTag = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-(alpha|beta|rc)(?:\.(0|[1-9][0-9]*))?)?$`)

func installerVersion(tag string) (string, error) {
	parts := releaseTag.FindStringSubmatch(tag)
	if parts == nil {
		return "", fmt.Errorf("release tag must be vmajor.minor.patch with optional -alpha, -beta or -rc and ordinal 0..29")
	}
	values := make([]int, 3)
	for i, bound := range []int{255, 255, 654} {
		value, err := strconv.Atoi(parts[i+1])
		if err != nil || value > bound {
			return "", fmt.Errorf("version component %d exceeds installer limit %d", i+1, bound)
		}
		values[i] = value
	}
	rank := 99 // final release sorts after every prerelease of the same patch
	if parts[4] != "" {
		rank = map[string]int{"alpha": 1, "beta": 33, "rc": 65}[parts[4]]
		if parts[5] != "" {
			ordinal, err := strconv.Atoi(parts[5])
			if err != nil || ordinal > 29 {
				return "", fmt.Errorf("prerelease ordinal must be between 0 and 29")
			}
			rank += ordinal + 1
		}
	}
	return fmt.Sprintf("%d.%d.%d", values[0], values[1], values[2]*100+rank), nil
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: release-version vmajor.minor.patch[-alpha|beta|rc[.n]]")
		os.Exit(1)
	}
	tag := os.Args[1]
	msi, err := installerVersion(tag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("VERSION=%s\nTAG_VERSION=%s\nMSI_VERSION=%s\n", tag[1:], tag, msi)
}
