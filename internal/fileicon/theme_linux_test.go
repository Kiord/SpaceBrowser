//go:build linux

package fileicon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxThemeResolverUsesInheritanceAndEscapedSVG(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	parent := filepath.Join(root, "parent")
	if err := os.MkdirAll(filepath.Join(parent, "32x32", "mimetypes"), 0o700); err != nil {
		t.Fatal(err)
	}
	childIndex := "[Icon Theme]\nName=Child\nInherits=parent\nDirectories=\n"
	parentIndex := "[Icon Theme]\nName=Parent\nDirectories=32x32/mimetypes\n\n[32x32/mimetypes]\nSize=32\nType=Fixed\n"
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "index.theme"), []byte(childIndex), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "index.theme"), []byte(parentIndex), 0o600); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(parent, "32x32", "mimetypes", "application-zip.svg")
	if err := os.WriteFile(want, []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver := &iconThemeResolver{
		roots:        []string{root},
		currentTheme: "child",
		targetSize:   32,
		themes:       make(map[string]iconThemeDefinition),
	}
	got, err := resolver.Resolve([]string{"application-zip"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != want {
		t.Fatalf("resolved path = %q, want %q", got, want)
	}
	icon, err := readIconFile(got)
	if err != nil {
		t.Fatalf("readIconFile: %v", err)
	}
	if icon.MediaType != "image/svg+xml" {
		t.Fatalf("media type = %q", icon.MediaType)
	}
}

func TestLinuxThemeResolverPrefersCurrentThemeBeforeCloserInheritedSize(t *testing.T) {
	root := t.TempDir()
	writeTheme := func(theme, inherits, directories string) {
		t.Helper()
		directory := filepath.Join(root, theme)
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		index := "[Icon Theme]\nName=" + theme + "\nInherits=" + inherits + "\nDirectories=" + directories + "\n"
		for _, name := range splitCommaList(directories) {
			index += "\n[" + name + "]\nSize=" + filepath.Base(filepath.Dir(name))[:2] + "\nType=Fixed\n"
		}
		if err := os.WriteFile(filepath.Join(directory, "index.theme"), []byte(index), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeTheme("current", "parent", "16x16/mimetypes")
	writeTheme("parent", "", "32x32/mimetypes")
	for theme, size := range map[string]string{"current": "16x16", "parent": "32x32"} {
		directory := filepath.Join(root, theme, size, "mimetypes")
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "text-plain.png"), []byte("png"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	resolver := &iconThemeResolver{roots: []string{root}, currentTheme: "current", targetSize: 32, themes: make(map[string]iconThemeDefinition)}
	got, err := resolver.Resolve([]string{"text-plain"})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "current", "16x16", "mimetypes", "text-plain.png")
	if got != want {
		t.Fatalf("resolved path = %q, want current-theme path %q", got, want)
	}
}
