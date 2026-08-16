package desktopportal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

type fakeCall struct {
	Destination string
	Path        dbus.ObjectPath
	Method      string
	Arguments   []any
}

type fakeTransport struct {
	mu sync.Mutex

	uniqueName  string
	supportsFDs bool
	owned       bool
	startErr    error
	versions    map[string]any
	versionErrs map[string]error

	responseCode    uint32
	responseResults map[string]dbus.Variant
	responseBody    []any
	sendResponse    bool
	disappear       bool
	returnedPath    dbus.ObjectPath
	requestStarted  chan struct{}
	requestOnce     sync.Once

	signals chan<- *dbus.Signal
	calls   []fakeCall
	rules   []signalRule
	removed []signalRule
	closed  dbus.ObjectPath
	ctx     context.Context
	cancel  context.CancelFunc
}

func newFakeTransport() *fakeTransport {
	ctx, cancel := context.WithCancel(context.Background())
	return &fakeTransport{
		uniqueName:      ":1.42",
		supportsFDs:     true,
		owned:           true,
		versions:        map[string]any{fileChooserInterface: uint32(3), openURIInterface: uint32(3)},
		versionErrs:     make(map[string]error),
		responseCode:    0,
		responseResults: make(map[string]dbus.Variant),
		sendResponse:    true,
		requestStarted:  make(chan struct{}),
		ctx:             ctx,
		cancel:          cancel,
	}
}

func (f *fakeTransport) Call(_ context.Context, destination string, path dbus.ObjectPath, method string, arguments []any, outputs ...any) error {
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{Destination: destination, Path: path, Method: method, Arguments: append([]any(nil), arguments...)})
	f.mu.Unlock()

	switch method {
	case "org.freedesktop.DBus.NameHasOwner":
		setFakeOutput(outputs, f.owned)
		return nil
	case "org.freedesktop.DBus.StartServiceByName":
		if f.startErr != nil {
			return f.startErr
		}
		setFakeOutput(outputs, uint32(1))
		return nil
	case "org.freedesktop.DBus.Properties.Get":
		interfaceName, _ := arguments[0].(string)
		if err := f.versionErrs[interfaceName]; err != nil {
			return err
		}
		version, ok := f.versions[interfaceName]
		if !ok {
			return dbus.NewError("org.freedesktop.DBus.Error.UnknownInterface", []any{"not available"})
		}
		setFakeOutput(outputs, dbus.MakeVariant(version))
		return nil
	case fileChooserInterface + ".OpenFile", openURIInterface + ".OpenFile":
		f.requestOnce.Do(func() { close(f.requestStarted) })
		options, _ := arguments[len(arguments)-1].(map[string]dbus.Variant)
		token, _ := options["handle_token"].Value().(string)
		expected, _ := requestObjectPath(f.uniqueName, token)
		returned := f.returnedPath
		if returned == "" {
			returned = expected
		}
		setFakeOutput(outputs, returned)
		if f.disappear {
			f.emit(&dbus.Signal{
				Path: dbusPath,
				Name: "org.freedesktop.DBus.NameOwnerChanged",
				Body: []any{portalService, ":1.7", ""},
			})
		} else if f.sendResponse {
			body := f.responseBody
			if body == nil {
				body = []any{f.responseCode, f.responseResults}
			}
			f.emit(&dbus.Signal{Path: returned, Name: requestInterface + ".Response", Body: body})
		}
		return nil
	case requestInterface + ".Close":
		f.mu.Lock()
		f.closed = path
		f.mu.Unlock()
		return nil
	default:
		return fmt.Errorf("unexpected fake call %s", method)
	}
}

func setFakeOutput(outputs []any, value any) {
	if len(outputs) != 1 {
		panic(fmt.Sprintf("fake output count = %d", len(outputs)))
	}
	switch output := outputs[0].(type) {
	case *bool:
		*output = value.(bool)
	case *uint32:
		*output = value.(uint32)
	case *dbus.Variant:
		*output = value.(dbus.Variant)
	case *dbus.ObjectPath:
		*output = value.(dbus.ObjectPath)
	default:
		panic(fmt.Sprintf("unsupported fake output %T", output))
	}
}

func (f *fakeTransport) emit(signal *dbus.Signal) {
	f.mu.Lock()
	channel := f.signals
	f.mu.Unlock()
	if channel != nil {
		channel <- signal
	}
}

func (f *fakeTransport) AddMatch(_ context.Context, rule signalRule) error {
	f.mu.Lock()
	f.rules = append(f.rules, rule)
	f.mu.Unlock()
	return nil
}

func (f *fakeTransport) RemoveMatch(rule signalRule) error {
	f.mu.Lock()
	f.removed = append(f.removed, rule)
	f.mu.Unlock()
	return nil
}

func (f *fakeTransport) Signal(channel chan<- *dbus.Signal) {
	f.mu.Lock()
	f.signals = channel
	f.mu.Unlock()
}

func (f *fakeTransport) RemoveSignal(channel chan<- *dbus.Signal) {
	f.mu.Lock()
	if f.signals == channel {
		f.signals = nil
	}
	f.mu.Unlock()
}

func (f *fakeTransport) UniqueName() string       { return f.uniqueName }
func (f *fakeTransport) SupportsUnixFDs() bool    { return f.supportsFDs }
func (f *fakeTransport) Context() context.Context { return f.ctx }
func (f *fakeTransport) Close() error             { f.cancel(); return nil }

func testClient(bus *fakeTransport) *Client {
	client := newClient(bus)
	client.tokenSource = func() (string, error) { return "test_token", nil }
	client.requestTimeout = time.Second
	return client
}

func TestEnsureServiceReportsUnavailablePortal(t *testing.T) {
	bus := newFakeTransport()
	bus.owned = false
	bus.startErr = dbus.NewError("org.freedesktop.DBus.Error.ServiceUnknown", []any{"not installed"})

	err := testClient(bus).ensureService(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ensureService error = %v, want ErrUnavailable", err)
	}
}

func TestPickDirectoryUsesExactRequestAndDecodesURI(t *testing.T) {
	bus := newFakeTransport()
	bus.responseResults["uris"] = dbus.MakeVariant([]string{"file:///tmp/My%20Folder"})
	client := testClient(bus)

	selected, err := client.PickDirectory(context.Background(), PickDirectoryOptions{Title: "Choose scan root"})
	if err != nil {
		t.Fatal(err)
	}
	if selected != filepath.FromSlash("/tmp/My Folder") {
		t.Fatalf("selected path = %q", selected)
	}

	expectedPath, _ := requestObjectPath(bus.uniqueName, "test_token")
	if !hasRule(bus.rules, signalRule{Path: expectedPath, Interface: requestInterface, Member: "Response"}) {
		t.Fatalf("exact response rule %q missing from %+v", expectedPath, bus.rules)
	}
	call := portalCall(t, bus)
	options, ok := call.Arguments[len(call.Arguments)-1].(map[string]dbus.Variant)
	if !ok || options["directory"].Value() != true || options["multiple"].Value() != false || options["handle_token"].Value() != "test_token" {
		t.Fatalf("folder chooser options = %+v", options)
	}
}

func TestPickDirectoryRejectsUnsupportedPortalVersionBeforeShowingDialog(t *testing.T) {
	bus := newFakeTransport()
	bus.versions[fileChooserInterface] = uint32(2)
	client := testClient(bus)

	_, err := client.PickDirectory(context.Background(), PickDirectoryOptions{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("PickDirectory error = %v, want ErrUnsupported", err)
	}
	if countPortalCalls(bus) != 0 {
		t.Fatal("folder chooser was invoked despite an unsupported interface version")
	}
}

func TestVersionQueryOperationalFailureDoesNotBecomeUnsupported(t *testing.T) {
	bus := newFakeTransport()
	bus.versionErrs[fileChooserInterface] = errors.New("session bus read failed")

	_, err := testClient(bus).PickDirectory(context.Background(), PickDirectoryOptions{})
	if err == nil || errors.Is(err, ErrUnsupported) || errors.Is(err, ErrUnavailable) {
		t.Fatalf("PickDirectory error = %v, want an operational failure", err)
	}
}

func TestPortalUserCancellationIsDistinct(t *testing.T) {
	bus := newFakeTransport()
	bus.responseCode = 1
	client := testClient(bus)

	_, err := client.PickDirectory(context.Background(), PickDirectoryOptions{})
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("PickDirectory error = %v, want ErrCancelled", err)
	}
}

func TestPickDirectoryRejectsMalformedResults(t *testing.T) {
	tests := []struct {
		name    string
		results map[string]dbus.Variant
		body    []any
	}{
		{name: "missing uris", results: map[string]dbus.Variant{}},
		{name: "wrong uris type", results: map[string]dbus.Variant{"uris": dbus.MakeVariant("file:///tmp")}},
		{name: "multiple selections", results: map[string]dbus.Variant{"uris": dbus.MakeVariant([]string{"file:///tmp/a", "file:///tmp/b"})}},
		{name: "remote URI", results: map[string]dbus.Variant{"uris": dbus.MakeVariant([]string{"file://server/share"})}},
		{name: "non-file URI", results: map[string]dbus.Variant{"uris": dbus.MakeVariant([]string{"https://example.com"})}},
		{name: "invalid response body", body: []any{"success"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bus := newFakeTransport()
			bus.responseResults = test.results
			bus.responseBody = test.body
			_, err := testClient(bus).PickDirectory(context.Background(), PickDirectoryOptions{})
			if !errors.Is(err, ErrMalformedResponse) {
				t.Fatalf("PickDirectory error = %v, want ErrMalformedResponse", err)
			}
		})
	}
}

func TestRequestReportsDisappearingPortal(t *testing.T) {
	bus := newFakeTransport()
	bus.disappear = true
	bus.sendResponse = false

	_, err := testClient(bus).PickDirectory(context.Background(), PickDirectoryOptions{})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("PickDirectory error = %v, want ErrUnavailable", err)
	}
}

func TestContextCancellationClosesPortalRequest(t *testing.T) {
	bus := newFakeTransport()
	bus.sendResponse = false
	client := testClient(bus)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-bus.requestStarted
		cancel()
	}()

	_, err := client.PickDirectory(ctx, PickDirectoryOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("PickDirectory error = %v, want context.Canceled", err)
	}
	expectedPath, _ := requestObjectPath(bus.uniqueName, "test_token")
	if bus.closed != expectedPath {
		t.Fatalf("closed request = %q, want %q", bus.closed, expectedPath)
	}
}

func TestRequestTimeoutClosesPortalRequest(t *testing.T) {
	bus := newFakeTransport()
	bus.sendResponse = false
	client := testClient(bus)
	client.requestTimeout = 10 * time.Millisecond

	_, err := client.PickDirectory(context.Background(), PickDirectoryOptions{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("PickDirectory error = %v, want context.DeadlineExceeded", err)
	}
	if bus.closed == "" {
		t.Fatal("timed-out request was not closed")
	}
}

func TestReturnedRequestPathIsSubscribedWhenDifferent(t *testing.T) {
	bus := newFakeTransport()
	bus.returnedPath = "/org/freedesktop/portal/desktop/request/legacy/request"
	bus.responseResults["uris"] = dbus.MakeVariant([]string{"file:///tmp"})
	client := testClient(bus)

	if _, err := client.PickDirectory(context.Background(), PickDirectoryOptions{}); err != nil {
		t.Fatal(err)
	}
	if !hasRule(bus.rules, signalRule{Path: bus.returnedPath, Interface: requestInterface, Member: "Response"}) {
		t.Fatalf("returned request rule %q missing from %+v", bus.returnedPath, bus.rules)
	}
}

func TestOpenFileRequiresUnixFDTransport(t *testing.T) {
	bus := newFakeTransport()
	bus.supportsFDs = false
	client := testClient(bus)

	err := client.OpenFile(context.Background(), filepath.Join(t.TempDir(), "missing"), OpenFileOptions{Ask: true})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("OpenFile error = %v, want ErrUnsupported", err)
	}
	if countPortalCalls(bus) != 0 {
		t.Fatal("OpenFile request was invoked without Unix FD support")
	}
}

func TestOpenFilePassesUnixFDAndAskOption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.zip")
	if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	bus := newFakeTransport()
	client := testClient(bus)

	if err := client.OpenFile(context.Background(), path, OpenFileOptions{Ask: true}); err != nil {
		t.Fatal(err)
	}
	call := portalCall(t, bus)
	if _, ok := call.Arguments[1].(dbus.UnixFD); !ok {
		t.Fatalf("OpenFile descriptor argument has type %T", call.Arguments[1])
	}
	options, ok := call.Arguments[len(call.Arguments)-1].(map[string]dbus.Variant)
	if !ok || options["ask"].Value() != true {
		t.Fatalf("OpenFile options = %+v", options)
	}
}

func TestOpenFileAskRequiresPortalVersionThree(t *testing.T) {
	path := filepath.Join(t.TempDir(), "content.txt")
	if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	bus := newFakeTransport()
	bus.versions[openURIInterface] = uint32(2)
	client := testClient(bus)

	if err := client.OpenFile(context.Background(), path, OpenFileOptions{Ask: true}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("OpenFile ask error = %v, want ErrUnsupported", err)
	}
	if err := client.OpenFile(context.Background(), path, OpenFileOptions{Ask: false}); err != nil {
		t.Fatalf("OpenFile default error = %v", err)
	}
}

func hasRule(rules []signalRule, wanted signalRule) bool {
	for _, rule := range rules {
		if rule == wanted {
			return true
		}
	}
	return false
}

func portalCall(t *testing.T, bus *fakeTransport) fakeCall {
	t.Helper()
	bus.mu.Lock()
	defer bus.mu.Unlock()
	for _, call := range bus.calls {
		if call.Destination == portalService && (call.Method == fileChooserInterface+".OpenFile" || call.Method == openURIInterface+".OpenFile") {
			return call
		}
	}
	t.Fatal("portal request call not found")
	return fakeCall{}
}

func countPortalCalls(bus *fakeTransport) int {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	count := 0
	for _, call := range bus.calls {
		if call.Destination == portalService && call.Method != "org.freedesktop.DBus.Properties.Get" {
			count++
		}
	}
	return count
}
