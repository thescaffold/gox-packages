// Package blobs is a Go HTTP client for the scaffold blobs server.
// It mirrors jsx-packages/libs/blobs behavior: chunked upload via init→batch→verify
// and download URL construction. Use BlobsModule.Register(cfg) to wire FilesService.
package blobs

import "github.com/thescaffold/gox-packages-blobs/files"

// Re-exports for single-import convenience.
type FilesService = files.FilesService
type File = files.File
type Page = files.Page
