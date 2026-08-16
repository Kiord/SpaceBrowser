package main

import "testing"

func TestPotentialSystemTrashNamesIncludePerUserRoots(t *testing.T) {
	for _, name := range []string{"$Recycle.Bin", "Trash", ".Trash", ".Trash-1000", ".Trashes", "501"} {
		if !isPotentialSystemTrashName(name) {
			t.Errorf("%q was not recognized as a potential system Trash name", name)
		}
	}
	for _, name := range []string{"Documents", ".Trash-not-a-uid", "501-backup"} {
		if isPotentialSystemTrashName(name) {
			t.Errorf("%q was recognized as a potential system Trash name", name)
		}
	}
}
