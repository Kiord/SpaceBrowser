//go:build darwin && cgo

#import <AppKit/AppKit.h>
#import <Foundation/Foundation.h>

#include <stdlib.h>
#include <string.h>

#include "backend_darwin.h"

static void sb_set_error(char **target, const char *message) {
    if (target != NULL) {
        *target = strdup(message);
    }
}

unsigned char *sb_file_icon_png(const char *path, size_t *length, char **error_message) {
    if (length != NULL) {
        *length = 0;
    }
    if (error_message != NULL) {
        *error_message = NULL;
    }
    if (path == NULL) {
        sb_set_error(error_message, "missing path");
        return NULL;
    }

    @autoreleasepool {
        NSString *filePath = [NSString stringWithUTF8String:path];
        if (filePath == nil) {
            sb_set_error(error_message, "path is not valid UTF-8");
            return NULL;
        }
        NSImage *source = [[NSWorkspace sharedWorkspace] iconForFile:filePath];
        if (source == nil) {
            sb_set_error(error_message, "NSWorkspace returned no icon");
            return NULL;
        }

        NSRect proposedRect = NSMakeRect(0, 0, 32, 32);
        CGImageRef image = [source CGImageForProposedRect:&proposedRect context:nil hints:nil];
        if (image == NULL) {
            sb_set_error(error_message, "icon has no bitmap representation");
            return NULL;
        }
        NSBitmapImageRep *representation = [[NSBitmapImageRep alloc] initWithCGImage:image];
        [representation setSize:NSMakeSize(32, 32)];
        NSData *png = [representation representationUsingType:NSBitmapImageFileTypePNG properties:@{}];
        [representation release];
        if (png == nil || [png length] == 0) {
            sb_set_error(error_message, "could not encode icon as PNG");
            return NULL;
        }

        unsigned char *result = malloc([png length]);
        if (result == NULL) {
            sb_set_error(error_message, "allocate icon buffer");
            return NULL;
        }
        memcpy(result, [png bytes], [png length]);
        if (length != NULL) {
            *length = [png length];
        }
        return result;
    }
}

void sb_icon_free(void *pointer) {
    free(pointer);
}
