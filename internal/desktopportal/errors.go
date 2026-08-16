package desktopportal

import "errors"

var (
	ErrUnavailable       = errors.New("desktop portal unavailable")
	ErrUnsupported       = errors.New("desktop portal operation unsupported")
	ErrCancelled         = errors.New("desktop portal request cancelled by user")
	ErrMalformedResponse = errors.New("desktop portal returned a malformed response")
	ErrRequestFailed     = errors.New("desktop portal request failed")
)
