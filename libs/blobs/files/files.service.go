package files

import "io"

// FilesService delegates Upload/Download to the configured StorageProvider.
type FilesService struct {
	provider StorageProvider
}

// NewFilesService creates a FilesService backed by provider.
func NewFilesService(provider StorageProvider) *FilesService {
	return &FilesService{provider: provider}
}

// Upload stores the content at input path (or reader) into parentId bucket with tags.
// input is treated as a local file path; callers that have an io.Reader should
// use UploadReader directly.
func (s *FilesService) Upload(input, parentID string, tags []string) (string, error) {
	return s.UploadReader(nil, input, parentID, tags)
}

// UploadReader uploads from r into bucket parentID with name and tags.
func (s *FilesService) UploadReader(r io.Reader, name, parentID string, tags []string) (string, error) {
	return s.provider.Upload(r, name, parentID, tags)
}

// Download retrieves the content identified by id.
func (s *FilesService) Download(id string) (io.ReadCloser, error) {
	return s.provider.Download(id)
}
