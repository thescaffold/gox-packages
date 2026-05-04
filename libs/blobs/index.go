// Package blobs provides file storage abstraction for the NTX platform.
// Use BlobsModule.Register(cfg) to wire LocalProvider or the S3Provider stub.
package blobs

import "github.com/thescaffold/gox-packages-blobs/files"

// Re-exports for single-import convenience.
type FilesService = files.FilesService
type StorageProvider = files.StorageProvider
