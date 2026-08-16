package desktopportal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	portalService       = "org.freedesktop.portal.Desktop"
	portalPath          = dbus.ObjectPath("/org/freedesktop/portal/desktop")
	requestInterface    = "org.freedesktop.portal.Request"
	dbusService         = "org.freedesktop.DBus"
	dbusPath            = dbus.ObjectPath("/org/freedesktop/DBus")
	defaultRequestLimit = 30 * time.Minute
)

type signalRule struct {
	Path      dbus.ObjectPath
	Interface string
	Member    string
	Arg0      string
}

type transport interface {
	Call(context.Context, string, dbus.ObjectPath, string, []any, ...any) error
	AddMatch(context.Context, signalRule) error
	RemoveMatch(signalRule) error
	Signal(chan<- *dbus.Signal)
	RemoveSignal(chan<- *dbus.Signal)
	UniqueName() string
	SupportsUnixFDs() bool
	Context() context.Context
	Close() error
}

type dbusTransport struct {
	conn *dbus.Conn
}

func (t *dbusTransport) Call(ctx context.Context, destination string, path dbus.ObjectPath, method string, args []any, outputs ...any) error {
	call := t.conn.Object(destination, path).CallWithContext(ctx, method, 0, args...)
	if call.Err != nil {
		return call.Err
	}
	return call.Store(outputs...)
}

func matchOptions(rule signalRule) []dbus.MatchOption {
	options := make([]dbus.MatchOption, 0, 4)
	if rule.Path != "" {
		options = append(options, dbus.WithMatchObjectPath(rule.Path))
	}
	if rule.Interface != "" {
		options = append(options, dbus.WithMatchInterface(rule.Interface))
	}
	if rule.Member != "" {
		options = append(options, dbus.WithMatchMember(rule.Member))
	}
	if rule.Arg0 != "" {
		options = append(options, dbus.WithMatchArg(0, rule.Arg0))
	}
	return options
}

func (t *dbusTransport) AddMatch(ctx context.Context, rule signalRule) error {
	return t.conn.AddMatchSignalContext(ctx, matchOptions(rule)...)
}

func (t *dbusTransport) RemoveMatch(rule signalRule) error {
	return t.conn.RemoveMatchSignal(matchOptions(rule)...)
}

func (t *dbusTransport) Signal(channel chan<- *dbus.Signal)       { t.conn.Signal(channel) }
func (t *dbusTransport) RemoveSignal(channel chan<- *dbus.Signal) { t.conn.RemoveSignal(channel) }
func (t *dbusTransport) SupportsUnixFDs() bool                    { return t.conn.SupportsUnixFDs() }
func (t *dbusTransport) Context() context.Context                 { return t.conn.Context() }
func (t *dbusTransport) Close() error                             { return t.conn.Close() }

func (t *dbusTransport) UniqueName() string {
	for _, name := range t.conn.Names() {
		if strings.HasPrefix(name, ":") {
			return name
		}
	}
	return ""
}

type Client struct {
	bus            transport
	requestTimeout time.Duration
	tokenSource    func() (string, error)

	versionMu sync.Mutex
	versions  map[string]uint32
}

func Connect(ctx context.Context) (*Client, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("%w: connect to session bus: %v", ErrUnavailable, err)
	}
	client := newClient(&dbusTransport{conn: conn})
	if err := client.ensureService(ctx); err != nil {
		conn.Close()
		return nil, err
	}
	return client, nil
}

func newClient(bus transport) *Client {
	return &Client{
		bus:            bus,
		requestTimeout: defaultRequestLimit,
		tokenSource:    newRequestToken,
		versions:       make(map[string]uint32),
	}
}

func (c *Client) Close() error {
	if c == nil || c.bus == nil {
		return nil
	}
	return c.bus.Close()
}

func (c *Client) ensureService(ctx context.Context) error {
	var owned bool
	if err := c.bus.Call(ctx, dbusService, dbusPath, "org.freedesktop.DBus.NameHasOwner", []any{portalService}, &owned); err != nil {
		return classifyDBusError("check desktop portal", err, ErrUnavailable)
	}
	if owned {
		return nil
	}

	var result uint32
	if err := c.bus.Call(ctx, dbusService, dbusPath, "org.freedesktop.DBus.StartServiceByName", []any{portalService, uint32(0)}, &result); err != nil {
		return classifyDBusError("start desktop portal", err, ErrUnavailable)
	}
	if result != 1 && result != 2 {
		return fmt.Errorf("%w: activation returned status %d", ErrUnavailable, result)
	}
	return nil
}

func (c *Client) interfaceVersion(ctx context.Context, interfaceName string) (uint32, error) {
	c.versionMu.Lock()
	if version, ok := c.versions[interfaceName]; ok {
		c.versionMu.Unlock()
		return version, nil
	}
	c.versionMu.Unlock()

	var value dbus.Variant
	err := c.bus.Call(ctx, portalService, portalPath, "org.freedesktop.DBus.Properties.Get", []any{interfaceName, "version"}, &value)
	if err != nil {
		return 0, classifyDBusError("query "+interfaceName+" version", err, nil)
	}
	version, ok := value.Value().(uint32)
	if !ok {
		return 0, fmt.Errorf("%w: %s version has type %T", ErrMalformedResponse, interfaceName, value.Value())
	}

	c.versionMu.Lock()
	c.versions[interfaceName] = version
	c.versionMu.Unlock()
	return version, nil
}

func (c *Client) requireVersion(ctx context.Context, interfaceName string, minimum uint32) error {
	version, err := c.interfaceVersion(ctx, interfaceName)
	if err != nil {
		return err
	}
	if version < minimum {
		return fmt.Errorf("%w: %s version %d, need at least %d", ErrUnsupported, interfaceName, version, minimum)
	}
	return nil
}

func classifyDBusError(operation string, err error, fallback error) error {
	if err == nil {
		return nil
	}
	name := dbusErrorName(err)
	switch name {
	case "org.freedesktop.DBus.Error.ServiceUnknown", "org.freedesktop.DBus.Error.NameHasNoOwner", "org.freedesktop.DBus.Error.Disconnected":
		return fmt.Errorf("%w: %s: %v", ErrUnavailable, operation, err)
	case "org.freedesktop.DBus.Error.UnknownInterface", "org.freedesktop.DBus.Error.UnknownMethod", "org.freedesktop.DBus.Error.UnknownProperty":
		return fmt.Errorf("%w: %s: %v", ErrUnsupported, operation, err)
	default:
		if fallback != nil {
			return fmt.Errorf("%w: %s: %v", fallback, operation, err)
		}
		return fmt.Errorf("%s: %w", operation, err)
	}
}

func dbusErrorName(err error) string {
	var pointer *dbus.Error
	if errors.As(err, &pointer) && pointer != nil {
		return pointer.Name
	}
	var value dbus.Error
	if errors.As(err, &value) {
		return value.Name
	}
	return ""
}
