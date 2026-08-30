#ifndef SPACEBROWSER_TREE_WATCHER_DARWIN_H
#define SPACEBROWSER_TREE_WATCHER_DARWIN_H

#include <stdint.h>

typedef struct SBTreeWatcher SBTreeWatcher;

SBTreeWatcher *SBTreeWatcherStart(const char *root, uintptr_t handle);
void SBTreeWatcherStop(SBTreeWatcher *watcher);

#endif
