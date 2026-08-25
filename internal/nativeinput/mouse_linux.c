//go:build linux && cgo

#include <gtk/gtk.h>

extern void spacebrowserAuxiliaryMouseButton(unsigned int button);

static gboolean spacebrowserButtonPress(GtkWidget *widget, GdkEventButton *event, gpointer data) {
    (void)widget;
    (void)data;
    if (event != NULL && event->type == GDK_BUTTON_PRESS && event->button >= 8) {
        // GDK/X11 numbers side buttons from 8, while DOM MouseEvent.button
        // numbers Back and Forward from 3.
        spacebrowserAuxiliaryMouseButton(event->button - 5);
    }
    return FALSE;
}

static GtkWidget *spacebrowserFindWebView(GtkWidget *widget, GType webviewType) {
    if (widget == NULL) return NULL;
    if (webviewType != 0 && G_TYPE_CHECK_INSTANCE_TYPE(widget, webviewType)) return widget;
    if (!GTK_IS_CONTAINER(widget)) return NULL;

    GList *children = gtk_container_get_children(GTK_CONTAINER(widget));
    GtkWidget *found = NULL;
    for (GList *child = children; child != NULL && found == NULL; child = child->next) {
        found = spacebrowserFindWebView(GTK_WIDGET(child->data), webviewType);
    }
    g_list_free(children);
    return found;
}

static gboolean spacebrowserInstallMouseCapture(gpointer data) {
    (void)data;
    static unsigned int attempts = 0;
    GType webviewType = g_type_from_name("WebKitWebView");
    GList *windows = gtk_window_list_toplevels();
    GtkWidget *webview = NULL;
    for (GList *entry = windows; entry != NULL && webview == NULL; entry = entry->next) {
        webview = spacebrowserFindWebView(GTK_WIDGET(entry->data), webviewType);
    }
    g_list_free(windows);

    if (webview != NULL) {
        g_signal_connect(webview, "button-press-event", G_CALLBACK(spacebrowserButtonPress), NULL);
        return G_SOURCE_REMOVE;
    }
    attempts++;
    return attempts < 100 ? G_SOURCE_CONTINUE : G_SOURCE_REMOVE;
}

void spacebrowserStartMouseCapture(void) {
    g_timeout_add(50, spacebrowserInstallMouseCapture, NULL);
}
