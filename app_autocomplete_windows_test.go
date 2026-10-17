//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAutocompleteUppercasesDriveLetter(t *testing.T) {
	root := t.TempDir()
	if len(root) < 2 || root[1] != ':' {
		t.Skip("requires drive-letter temp directory")
	}
	child := filepath.Join(root, "MixedCase")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	query := strings.ToLower(root[:1]) + root[1:] + string(filepath.Separator)
	got := (&App{}).SuggestFolders(query)
	want := strings.ToUpper(child[:1]) + child[1:] + string(filepath.Separator)
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %v, want %q", got, want)
	}
}
