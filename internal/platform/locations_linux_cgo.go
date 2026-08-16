//go:build linux && cgo

package platform

/*
#cgo pkg-config: gio-2.0
#include <gio/gio.h>

static char *sb_scan_locations(void) {
    GVolumeMonitor *monitor = g_volume_monitor_get();
    if (monitor == NULL) {
        return NULL;
    }
    GList *mounts = g_volume_monitor_get_mounts(monitor);
    GString *result = g_string_new(NULL);

    for (GList *entry = mounts; entry != NULL; entry = entry->next) {
        GMount *mount = G_MOUNT(entry->data);
        GFile *root = g_mount_get_root(mount);
        char *path = root == NULL ? NULL : g_file_get_path(root);
        if (path != NULL) {
            char *name = g_mount_get_name(mount);
            const char *kind = "volume";
            GVolume *volume = g_mount_get_volume(mount);
            GDrive *drive = volume == NULL ? NULL : g_volume_get_drive(volume);
            if ((drive != NULL && g_drive_is_removable(drive)) || g_mount_can_eject(mount)) {
                kind = "removable";
            }

            char *escaped_name = g_uri_escape_string(name == NULL ? "" : name, NULL, TRUE);
            char *escaped_path = g_uri_escape_string(path, NULL, TRUE);
            if (result->len > 0) {
                g_string_append_c(result, '\n');
            }
            g_string_append_printf(result, "%s\t%s\t%s", escaped_name, escaped_path, kind);

            g_free(escaped_name);
            g_free(escaped_path);
            if (drive != NULL) g_object_unref(drive);
            if (volume != NULL) g_object_unref(volume);
            g_free(name);
            g_free(path);
        }
        if (root != NULL) g_object_unref(root);
    }

    g_list_free_full(mounts, g_object_unref);
    g_object_unref(monitor);
    return g_string_free(result, FALSE);
}

static void sb_scan_locations_free(void *pointer) {
    g_free(pointer);
}
*/
import "C"

import (
	"fmt"
	"net/url"
	"strings"
	"unsafe"
)

func linuxDesktopScanLocations() ([]ScanLocation, error) {
	value := C.sb_scan_locations()
	if value == nil {
		return nil, fmt.Errorf("GIO volume monitor is unavailable")
	}
	defer C.sb_scan_locations_free(unsafe.Pointer(value))

	text := C.GoString(value)
	if text == "" {
		return nil, nil
	}
	locations := make([]ScanLocation, 0)
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			continue
		}
		name, nameErr := url.PathUnescape(fields[0])
		path, pathErr := url.PathUnescape(fields[1])
		if nameErr != nil || pathErr != nil {
			continue
		}
		locations = append(locations, ScanLocation{Name: name, Path: path, Kind: fields[2]})
	}
	return locations, nil
}
