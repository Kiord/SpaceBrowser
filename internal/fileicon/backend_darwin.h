#ifndef SPACEBROWSER_FILEICON_DARWIN_H
#define SPACEBROWSER_FILEICON_DARWIN_H

#include <stddef.h>

unsigned char *sb_file_icon_png(const char *path, size_t *length, char **error_message);
void sb_icon_free(void *pointer);

#endif
