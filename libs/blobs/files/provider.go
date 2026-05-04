package files

import "io"

// StorageProvider is the interface every storage backend must implement.
type StorageProvider interface {
	// Upload stores data from r into bucket under name with optional tags.
	// Returns the public URL (or local path) on success.
	Upload(r io.Reader, name, bucket string, tags []string) (url string, err error)

	// Download retrieves the content identified by id.
	Download(id string) (io.ReadCloser, error)
}
