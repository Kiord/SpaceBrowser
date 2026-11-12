package platform

// ApplicationMetadata is optional read-only desktop integration. Path names an
// executable, application bundle, or desktop entry whose own icon can be read.
type Application struct {
	Name string
	Path string
}

type ApplicationMetadata interface {
	DefaultApplicationPath(string) (string, error)
	FileBrowserApplication() (Application, error)
}
