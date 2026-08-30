//go:build darwin && cgo

#include "tree_watcher_darwin.h"

#include <CoreServices/CoreServices.h>
#include <dispatch/dispatch.h>
#include <stdlib.h>

extern void goTreeWatcherEvent(uintptr_t handle, char *path, uint32_t flags);

struct SBTreeWatcher {
    FSEventStreamRef stream;
    dispatch_queue_t queue;
};

static void sbTreeWatcherCallback(
    ConstFSEventStreamRef stream,
    void *context,
    size_t eventCount,
    void *eventPaths,
    const FSEventStreamEventFlags eventFlags[],
    const FSEventStreamEventId eventIDs[]
) {
    (void)stream;
    (void)eventIDs;
    uintptr_t handle = (uintptr_t)context;
    char **paths = (char **)eventPaths;
    for (size_t index = 0; index < eventCount; index++) {
        goTreeWatcherEvent(handle, paths[index], (uint32_t)eventFlags[index]);
    }
}

SBTreeWatcher *SBTreeWatcherStart(const char *root, uintptr_t handle) {
    if (root == NULL) {
        return NULL;
    }
    CFStringRef rootString = CFStringCreateWithCString(kCFAllocatorDefault, root, kCFStringEncodingUTF8);
    if (rootString == NULL) {
        return NULL;
    }
    const void *values[] = {rootString};
    CFArrayRef paths = CFArrayCreate(kCFAllocatorDefault, values, 1, &kCFTypeArrayCallBacks);
    CFRelease(rootString);
    if (paths == NULL) {
        return NULL;
    }

    FSEventStreamContext context = {0, (void *)handle, NULL, NULL, NULL};
    FSEventStreamCreateFlags flags =
        kFSEventStreamCreateFlagWatchRoot |
        kFSEventStreamCreateFlagFileEvents |
        kFSEventStreamCreateFlagNoDefer;
    FSEventStreamRef stream = FSEventStreamCreate(
        kCFAllocatorDefault,
        sbTreeWatcherCallback,
        &context,
        paths,
        kFSEventStreamEventIdSinceNow,
        0.20,
        flags
    );
    CFRelease(paths);
    if (stream == NULL) {
        return NULL;
    }

    dispatch_queue_t queue = dispatch_queue_create("org.spacebrowser.scan-cache", DISPATCH_QUEUE_SERIAL);
    if (queue == NULL) {
        FSEventStreamRelease(stream);
        return NULL;
    }
    FSEventStreamSetDispatchQueue(stream, queue);
    if (!FSEventStreamStart(stream)) {
        FSEventStreamInvalidate(stream);
        FSEventStreamRelease(stream);
        dispatch_release(queue);
        return NULL;
    }

    SBTreeWatcher *watcher = (SBTreeWatcher *)calloc(1, sizeof(SBTreeWatcher));
    if (watcher == NULL) {
        FSEventStreamStop(stream);
        FSEventStreamInvalidate(stream);
        FSEventStreamRelease(stream);
        dispatch_release(queue);
        return NULL;
    }
    watcher->stream = stream;
    watcher->queue = queue;
    return watcher;
}

void SBTreeWatcherStop(SBTreeWatcher *watcher) {
    if (watcher == NULL) {
        return;
    }
    FSEventStreamStop(watcher->stream);
    FSEventStreamInvalidate(watcher->stream);
    FSEventStreamRelease(watcher->stream);
    dispatch_release(watcher->queue);
    free(watcher);
}
