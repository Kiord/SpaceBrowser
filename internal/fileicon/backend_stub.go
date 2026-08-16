//go:build (!windows && !darwin && !linux) || ((darwin || linux) && !cgo)

package fileicon

type unavailableBackend struct{}

func newPlatformBackend() backend { return unavailableBackend{} }

func (unavailableBackend) Lookup(string, bool) (Icon, error) {
	return Icon{}, ErrUnavailable
}
