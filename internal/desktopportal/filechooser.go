package desktopportal

import (
	"context"
	"fmt"
	"net/url"
	pathpkg "path"
	"path/filepath"
	"strings"

	"github.com/godbus/dbus/v5"
)

const fileChooserInterface = "org.freedesktop.portal.FileChooser"

type PickDirectoryOptions struct {
	Title         string
	ParentWindow  string
	CurrentFolder string
}

func (c *Client) PickDirectory(ctx context.Context, options PickDirectoryOptions) (string, error) {
	portalOptions := map[string]dbus.Variant{
		"directory": dbus.MakeVariant(true),
		"multiple":  dbus.MakeVariant(false),
		"modal":     dbus.MakeVariant(true),
	}
	if options.CurrentFolder != "" {
		folder := append([]byte(options.CurrentFolder), 0)
		portalOptions["current_folder"] = dbus.MakeVariant(folder)
	}
	title := strings.TrimSpace(options.Title)
	if title == "" {
		title = "Choose a folder"
	}

	result, err := c.request(ctx, fileChooserInterface, "OpenFile", 3, []any{options.ParentWindow, title}, portalOptions)
	if err != nil {
		return "", err
	}
	variant, ok := result.Results["uris"]
	if !ok {
		return "", fmt.Errorf("%w: folder chooser response has no uris", ErrMalformedResponse)
	}
	uris, ok := variant.Value().([]string)
	if !ok || len(uris) != 1 {
		return "", fmt.Errorf("%w: folder chooser returned %T with %d selections", ErrMalformedResponse, variant.Value(), len(uris))
	}
	return decodeLocalFileURI(uris[0])
}

func decodeLocalFileURI(value string) (string, error) {
	uri, err := url.Parse(value)
	if err != nil || uri.Scheme != "file" || uri.Opaque != "" || uri.RawQuery != "" || uri.Fragment != "" {
		return "", fmt.Errorf("%w: invalid local file URI %q", ErrMalformedResponse, value)
	}
	if uri.Host != "" && !strings.EqualFold(uri.Host, "localhost") {
		return "", fmt.Errorf("%w: remote file URI %q", ErrMalformedResponse, value)
	}
	path, err := url.PathUnescape(uri.EscapedPath())
	if err != nil || !pathpkg.IsAbs(path) || strings.ContainsRune(path, 0) {
		return "", fmt.Errorf("%w: invalid file URI path %q", ErrMalformedResponse, value)
	}
	return filepath.FromSlash(path), nil
}
