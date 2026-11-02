package platform

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/shirou/gopsutil/v3/disk"
)

func TestNestedVolumeMountsRespectsPathBoundaries(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "external")
	partitions := []disk.PartitionStat{
		{Mountpoint: root}, {Mountpoint: nested}, {Mountpoint: root + "-sibling"}, {Mountpoint: filepath.Dir(root)},
	}
	if mounts := nestedVolumeMounts(root, partitions, false); !reflect.DeepEqual(mounts, []string{nested}) {
		t.Fatalf("nested mounts = %v", mounts)
	}
}

func TestMacStartupScanIncludesDataButExcludesOtherMounts(t *testing.T) {
	partitions := []disk.PartitionStat{
		{Mountpoint: "/"}, {Mountpoint: "/System/Volumes/Data"},
		{Mountpoint: "/dev"}, {Mountpoint: "/Volumes/External"}, {Mountpoint: "/System/Volumes/Preboot"},
	}
	want := []string{filepath.FromSlash("/dev"), filepath.FromSlash("/Volumes/External"), filepath.FromSlash("/System/Volumes/Preboot")}
	if mounts := nestedVolumeMounts("/", partitions, true); !reflect.DeepEqual(mounts, want) {
		t.Fatalf("startup scan mounts = %v, want %v", mounts, want)
	}
}
