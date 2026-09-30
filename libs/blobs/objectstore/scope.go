package objectstore

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// WorkspacePrefix is the tenant prefix of the key layout (TRD §6.10):
// ws/<workspaceId>/...
func WorkspacePrefix(workspaceID string) string { return "ws/" + workspaceID + "/" }

// ForWorkspace confines base to one tenant: every key and prefix must lie
// under ws/<workspaceID>/, or the call fails with ErrForbidden before it
// reaches the driver. This is the tenant boundary enforced inside the storage
// layer itself, identical on every backend, so a bug (or a hostile key) in a
// caller cannot read or overwrite another workspace's bytes.
func ForWorkspace(base ObjectStore, workspaceID string) (ObjectStore, error) {
	if workspaceID == "" || strings.ContainsAny(workspaceID, "/\\") || ValidateKey(workspaceID) != nil {
		return nil, fmt.Errorf("%w: bad workspace id", ErrInvalidKey)
	}
	return &scoped{base: base, prefix: WorkspacePrefix(workspaceID)}, nil
}

type scoped struct {
	base   ObjectStore
	prefix string
}

func (s *scoped) key(k string) error {
	if err := ValidateKey(k); err != nil {
		return err
	}
	if !strings.HasPrefix(k, s.prefix) || len(k) == len(s.prefix) {
		return fmt.Errorf("%w: %q", ErrForbidden, k)
	}
	return nil
}

func (s *scoped) Put(ctx context.Context, key string, r io.Reader, o PutOptions) (ObjectInfo, error) {
	if err := s.key(key); err != nil {
		return ObjectInfo{}, err
	}
	return s.base.Put(ctx, key, r, o)
}
func (s *scoped) Get(ctx context.Context, key string, rng *Range) (io.ReadCloser, ObjectInfo, error) {
	if err := s.key(key); err != nil {
		return nil, ObjectInfo{}, err
	}
	return s.base.Get(ctx, key, rng)
}
func (s *scoped) Head(ctx context.Context, key string) (ObjectInfo, error) {
	if err := s.key(key); err != nil {
		return ObjectInfo{}, err
	}
	return s.base.Head(ctx, key)
}
func (s *scoped) Delete(ctx context.Context, key string) error {
	if err := s.key(key); err != nil {
		return err
	}
	return s.base.Delete(ctx, key)
}
func (s *scoped) List(ctx context.Context, prefix string, o ListOptions) (ListPage, error) {
	if prefix == "" {
		prefix = s.prefix // "everything I may see"
	}
	if err := ValidatePrefix(prefix); err != nil {
		return ListPage{}, err
	}
	if !strings.HasPrefix(prefix, s.prefix) && !strings.HasPrefix(s.prefix, prefix+"/") {
		return ListPage{}, fmt.Errorf("%w: %q", ErrForbidden, prefix)
	}
	if !strings.HasPrefix(prefix, s.prefix) { // a parent of my prefix: narrow it
		prefix = s.prefix
	}
	if o.After != "" && !strings.HasPrefix(o.After, s.prefix) {
		return ListPage{}, fmt.Errorf("%w: cursor", ErrForbidden)
	}
	return s.base.List(ctx, prefix, o)
}
func (s *scoped) Copy(ctx context.Context, src, dst string) (ObjectInfo, error) {
	if err := s.key(src); err != nil {
		return ObjectInfo{}, err
	}
	if err := s.key(dst); err != nil {
		return ObjectInfo{}, err
	}
	return s.base.Copy(ctx, src, dst)
}
func (s *scoped) Presign(ctx context.Context, key string, o PresignOptions) (string, error) {
	if err := s.key(key); err != nil {
		return "", err
	}
	return s.base.Presign(ctx, key, o)
}
func (s *scoped) BeginMultipart(ctx context.Context, key string, o PutOptions) (UploadID, error) {
	if err := s.key(key); err != nil {
		return "", err
	}
	return s.base.BeginMultipart(ctx, key, o)
}
func (s *scoped) UploadPart(ctx context.Context, id UploadID, n int, r io.Reader) (PartInfo, error) {
	return s.base.UploadPart(ctx, id, n, r)
}
func (s *scoped) CompleteMultipart(ctx context.Context, id UploadID, parts []PartInfo) (ObjectInfo, error) {
	return s.base.CompleteMultipart(ctx, id, parts)
}
func (s *scoped) AbortMultipart(ctx context.Context, id UploadID) error {
	return s.base.AbortMultipart(ctx, id)
}
