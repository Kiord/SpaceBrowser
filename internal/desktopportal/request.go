package desktopportal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

type requestResult struct {
	Results map[string]dbus.Variant
}

func newRequestToken() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate portal request token: %w", err)
	}
	return "spacebrowser_" + hex.EncodeToString(random), nil
}

func requestObjectPath(uniqueName, token string) (dbus.ObjectPath, error) {
	if !strings.HasPrefix(uniqueName, ":") || token == "" {
		return "", fmt.Errorf("%w: invalid D-Bus sender or request token", ErrMalformedResponse)
	}
	sender := strings.ReplaceAll(strings.TrimPrefix(uniqueName, ":"), ".", "_")
	path := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/" + sender + "/" + token)
	if !path.IsValid() {
		return "", fmt.Errorf("%w: invalid request object path %q", ErrMalformedResponse, path)
	}
	return path, nil
}

func (c *Client) request(ctx context.Context, interfaceName, method string, minimumVersion uint32, arguments []any, options map[string]dbus.Variant) (requestResult, error) {
	if err := c.requireVersion(ctx, interfaceName, minimumVersion); err != nil {
		return requestResult{}, err
	}
	token, err := c.tokenSource()
	if err != nil {
		return requestResult{}, err
	}
	expectedPath, err := requestObjectPath(c.bus.UniqueName(), token)
	if err != nil {
		return requestResult{}, err
	}

	requestCtx := ctx
	cancel := func() {}
	if c.requestTimeout > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, c.requestTimeout)
	}
	defer cancel()

	responseRule := signalRule{Path: expectedPath, Interface: requestInterface, Member: "Response"}
	ownerRule := signalRule{Path: dbusPath, Interface: "org.freedesktop.DBus", Member: "NameOwnerChanged", Arg0: portalService}
	responses := make(chan *dbus.Signal, 8)
	c.bus.Signal(responses)
	defer c.bus.RemoveSignal(responses)
	if err := c.bus.AddMatch(requestCtx, responseRule); err != nil {
		return requestResult{}, classifyDBusError("subscribe to portal response", err, nil)
	}
	defer c.bus.RemoveMatch(responseRule)
	if err := c.bus.AddMatch(requestCtx, ownerRule); err != nil {
		return requestResult{}, classifyDBusError("monitor desktop portal", err, nil)
	}
	defer c.bus.RemoveMatch(ownerRule)

	requestOptions := make(map[string]dbus.Variant, len(options)+1)
	for key, value := range options {
		requestOptions[key] = value
	}
	requestOptions["handle_token"] = dbus.MakeVariant(token)
	callArguments := append(append([]any(nil), arguments...), requestOptions)

	var returnedPath dbus.ObjectPath
	if err := c.bus.Call(requestCtx, portalService, portalPath, interfaceName+"."+method, callArguments, &returnedPath); err != nil {
		return requestResult{}, classifyDBusError("start portal request", err, nil)
	}
	if !returnedPath.IsValid() {
		return requestResult{}, fmt.Errorf("%w: portal returned invalid request path %q", ErrMalformedResponse, returnedPath)
	}

	activePath := returnedPath
	if returnedPath != expectedPath {
		rule := signalRule{Path: returnedPath, Interface: requestInterface, Member: "Response"}
		if err := c.bus.AddMatch(requestCtx, rule); err != nil {
			return requestResult{}, classifyDBusError("subscribe to returned portal request", err, nil)
		}
		defer c.bus.RemoveMatch(rule)
	}

	for {
		select {
		case signal, ok := <-responses:
			if !ok {
				return requestResult{}, fmt.Errorf("%w: session bus signal channel closed", ErrUnavailable)
			}
			if portalServiceDisappeared(signal) {
				return requestResult{}, fmt.Errorf("%w: desktop portal service disappeared", ErrUnavailable)
			}
			if signal == nil || signal.Path != activePath || signal.Name != requestInterface+".Response" {
				continue
			}
			var response uint32
			var results map[string]dbus.Variant
			if err := dbus.Store(signal.Body, &response, &results); err != nil {
				return requestResult{}, fmt.Errorf("%w: decode response: %v", ErrMalformedResponse, err)
			}
			switch response {
			case 0:
				return requestResult{Results: results}, nil
			case 1:
				return requestResult{}, ErrCancelled
			default:
				return requestResult{}, fmt.Errorf("%w: response code %d", ErrRequestFailed, response)
			}
		case <-requestCtx.Done():
			c.closeRequest(activePath)
			return requestResult{}, fmt.Errorf("portal request interrupted: %w", requestCtx.Err())
		case <-c.bus.Context().Done():
			return requestResult{}, fmt.Errorf("%w: session bus connection closed", ErrUnavailable)
		}
	}
}

func (c *Client) closeRequest(path dbus.ObjectPath) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.bus.Call(ctx, portalService, path, requestInterface+".Close", nil)
}

func portalServiceDisappeared(signal *dbus.Signal) bool {
	if signal == nil || signal.Name != "org.freedesktop.DBus.NameOwnerChanged" || len(signal.Body) != 3 {
		return false
	}
	name, nameOK := signal.Body[0].(string)
	newOwner, ownerOK := signal.Body[2].(string)
	return nameOK && ownerOK && name == portalService && newOwner == ""
}
