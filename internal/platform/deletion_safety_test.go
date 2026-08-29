package platform

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type deletionTestFileInfo struct {
	mode os.FileMode
}

func (info deletionTestFileInfo) Name() string       { return "item" }
func (info deletionTestFileInfo) Size() int64        { return 0 }
func (info deletionTestFileInfo) Mode() os.FileMode  { return info.mode }
func (info deletionTestFileInfo) ModTime() time.Time { return time.Time{} }
func (info deletionTestFileInfo) IsDir() bool        { return info.mode.IsDir() }
func (info deletionTestFileInfo) Sys() any           { return nil }

func TestValidateDeletionTargetRejectsProtectedTreesWithoutNameMatching(t *testing.T) {
	base := t.TempDir()
	protected := filepath.Join(base, "system")
	inside := filepath.Join(protected, "nested")
	lookalike := filepath.Join(base, "ordinary", "system")
	info := deletionTestFileInfo{mode: os.ModeDir}
	for _, directory := range []string{inside, lookalike} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	if err := validateDeletionTarget(inside, info, []string{protected}, nil, nil, false); err == nil || !strings.Contains(err.Error(), protectedDeletionMessage) {
		t.Fatalf("protected path error = %v", err)
	}
	if err := validateDeletionTarget(lookalike, info, []string{protected}, nil, nil, false); err != nil {
		t.Fatalf("lookalike folder was rejected: %v", err)
	}
}

func TestValidateDeletionTargetRejectsMountRootAndContainingDirectory(t *testing.T) {
	base := t.TempDir()
	mount := filepath.Join(base, "data", "mounted")
	info := deletionTestFileInfo{mode: os.ModeDir}
	if err := os.MkdirAll(mount, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := validateDeletionTarget(mount, info, nil, nil, []string{mount}, false); err == nil || !strings.Contains(err.Error(), mountRootDeletionMessage) {
		t.Fatalf("mount-root error = %v", err)
	}
	if err := validateDeletionTarget(filepath.Dir(mount), info, nil, nil, []string{mount}, false); err == nil || !strings.Contains(err.Error(), mountChildDeletionMessage) {
		t.Fatalf("mount-containing error = %v", err)
	}
}

func TestValidateDeletionFileTypeRejectsSpecialObjectsButAllowsSymlinks(t *testing.T) {
	for _, mode := range []os.FileMode{os.ModeNamedPipe, os.ModeSocket, os.ModeDevice} {
		if err := validateDeletionFileType(deletionTestFileInfo{mode: mode}); err == nil || !strings.Contains(err.Error(), specialDeletionMessage) {
			t.Fatalf("special mode %v error = %v", mode, err)
		}
	}
	if err := validateDeletionFileType(deletionTestFileInfo{mode: os.ModeSymlink}); err != nil {
		t.Fatalf("symlink rejected: %v", err)
	}
}

func TestPathWithinDoesNotAcceptSiblingPrefix(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "sys")
	if pathWithin(filepath.Join(base, "system", "file"), root, false) {
		t.Fatal("sibling prefix was classified as a descendant")
	}
	if !pathWithin(filepath.Join(root, "file"), root, false) {
		t.Fatal("actual descendant was not classified")
	}
	if !pathWithin(strings.ToUpper(filepath.Join(root, "file")), strings.ToLower(root), true) {
		t.Fatal("case-insensitive descendant was not classified")
	}
}

func TestDefaultValidateDeletionReportsMissingPath(t *testing.T) {
	err := (Default{}).ValidateDeletion(filepath.Join(t.TempDir(), "missing"))
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing-path error = %v", err)
	}
}

func TestValidateDeletionTargetResolvesParentSymlinks(t *testing.T) {
	base := t.TempDir()
	protected := filepath.Join(base, "protected")
	target := filepath.Join(protected, "nested")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(protected, alias); err != nil {
		t.Skipf("directory symlinks are unavailable: %v", err)
	}
	aliasedTarget := filepath.Join(alias, "nested")
	info, err := os.Lstat(aliasedTarget)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := physicalDeletionPath(aliasedTarget, info); err != nil {
		t.Skipf("symlink resolution is unavailable: %v", err)
	}
	if err := validateDeletionTarget(aliasedTarget, info, []string{protected}, nil, nil, false); err == nil || !strings.Contains(err.Error(), protectedDeletionMessage) {
		t.Fatalf("aliased protected path error = %v", err)
	}
}

func TestValidateDeletionTargetDoesNotFollowFinalSymlink(t *testing.T) {
	base := t.TempDir()
	protected := filepath.Join(base, "protected")
	safe := filepath.Join(base, "safe")
	if err := os.MkdirAll(protected, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(safe, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(safe, "protected-link")
	if err := os.Symlink(protected, link); err != nil {
		t.Skipf("directory symlinks are unavailable: %v", err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := physicalDeletionPath(link, info); err != nil {
		t.Skipf("symlink resolution is unavailable: %v", err)
	}
	if err := validateDeletionTarget(link, info, []string{protected}, nil, nil, false); err != nil {
		t.Fatalf("final symlink was rejected: %v", err)
	}
}

func TestValidateDeletionTargetChecksMountsThroughParentSymlink(t *testing.T) {
	base := t.TempDir()
	physicalContainer := filepath.Join(base, "physical", "container")
	mount := filepath.Join(physicalContainer, "mounted")
	if err := os.MkdirAll(mount, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(filepath.Join(base, "physical"), alias); err != nil {
		t.Skipf("directory symlinks are unavailable: %v", err)
	}
	aliasedContainer := filepath.Join(alias, "container")
	info, err := os.Lstat(aliasedContainer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := physicalDeletionPath(aliasedContainer, info); err != nil {
		t.Skipf("symlink resolution is unavailable: %v", err)
	}
	if err := validateDeletionTarget(aliasedContainer, info, nil, nil, []string{mount}, false); err == nil || !strings.Contains(err.Error(), mountChildDeletionMessage) {
		t.Fatalf("aliased mount-containing path error = %v", err)
	}
}
