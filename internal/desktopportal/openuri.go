package desktopportal

import (
	"context"
	"fmt"
	"os"

	"github.com/godbus/dbus/v5"
)

const openURIInterface = "org.freedesktop.portal.OpenURI"

type OpenFileOptions struct {
	Ask          bool
	ParentWindow string
}

func (c *Client) OpenFile(ctx context.Context, path string, options OpenFileOptions) error {
	minimumVersion := uint32(2)
	if options.Ask {
		minimumVersion = 3
	}
	if err := c.requireVersion(ctx, openURIInterface, minimumVersion); err != nil {
		return err
	}
	if !c.bus.SupportsUnixFDs() {
		return fmt.Errorf("%w: session bus does not support Unix file descriptors", ErrUnsupported)
	}

	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open portal item: %w", err)
	}
	defer file.Close()

	_, err = c.request(ctx, openURIInterface, "OpenFile", minimumVersion,
		[]any{options.ParentWindow, dbus.UnixFD(file.Fd())},
		map[string]dbus.Variant{"ask": dbus.MakeVariant(options.Ask)},
	)
	return err
}
