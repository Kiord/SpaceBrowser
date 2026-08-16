//go:build linux && cgo

package fileicon

/*
#cgo pkg-config: gio-2.0
#include <gio/gio.h>
#include <stdlib.h>

enum {
    SB_ICON_NONE = 0,
    SB_ICON_THEME = 1,
    SB_ICON_FILE = 2
};

static char *sb_gio_icon_value(const char *path, int *kind, char **error_message) {
    *kind = SB_ICON_NONE;
    *error_message = NULL;
    GFile *file = g_file_new_for_path(path);
    GError *error = NULL;
    GFileInfo *info = g_file_query_info(file, G_FILE_ATTRIBUTE_STANDARD_ICON,
        G_FILE_QUERY_INFO_NONE, NULL, &error);
    g_object_unref(file);
    if (info == NULL) {
        if (error != NULL) {
            *error_message = g_strdup(error->message);
            g_error_free(error);
        }
        return NULL;
    }
    GIcon *icon = g_file_info_get_icon(info);
    char *result = NULL;
    if (icon != NULL && G_IS_THEMED_ICON(icon)) {
        const char * const *names = g_themed_icon_get_names(G_THEMED_ICON(icon));
        GString *joined = g_string_new(NULL);
        for (int index = 0; names != NULL && names[index] != NULL; index++) {
            if (joined->len > 0) {
                g_string_append_c(joined, '\n');
            }
            g_string_append(joined, names[index]);
        }
        result = g_string_free(joined, FALSE);
        *kind = SB_ICON_THEME;
    } else if (icon != NULL && G_IS_FILE_ICON(icon)) {
        GFile *icon_file = g_file_icon_get_file(G_FILE_ICON(icon));
        result = g_file_get_path(icon_file);
        *kind = SB_ICON_FILE;
    }
    g_object_unref(info);
    return result;
}

static void sb_gio_free(void *pointer) {
    g_free(pointer);
}
*/
import "C"

import (
	"fmt"
	"strings"
	"unsafe"
)

type linuxBackend struct {
	resolver *iconThemeResolver
}

func newPlatformBackend() backend {
	return &linuxBackend{resolver: newIconThemeResolver()}
}

func (b *linuxBackend) Lookup(path string, _ bool) (Icon, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	var kind C.int
	var errorMessage *C.char
	value := C.sb_gio_icon_value(cPath, &kind, &errorMessage)
	if errorMessage != nil {
		defer C.sb_gio_free(unsafe.Pointer(errorMessage))
	}
	if value == nil {
		if errorMessage != nil {
			return Icon{}, fmt.Errorf("query GIO file icon: %s", C.GoString(errorMessage))
		}
		return Icon{}, ErrUnavailable
	}
	defer C.sb_gio_free(unsafe.Pointer(value))
	iconValue := C.GoString(value)
	switch int(kind) {
	case int(C.SB_ICON_FILE):
		return readIconFile(iconValue)
	case int(C.SB_ICON_THEME):
		names := strings.Split(iconValue, "\n")
		iconPath, err := b.resolver.Resolve(names)
		if err != nil {
			return Icon{}, err
		}
		return readIconFile(iconPath)
	default:
		return Icon{}, ErrUnavailable
	}
}
