// Package s3ws is the S3-compatible workspace Store (plan §4.7 priority
// 2: institutional object storage, MinIO, Cloudflare R2). The remote URL
// form is s3://<endpoint>/<bucket>[/<prefix>] (D34); credentials come
// from the per-remote token as "ACCESSKEY:SECRETKEY", falling back to
// AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY.
//
// Simple-PUT ETags are MD5s and are surfaced; multipart ETags are not,
// so those objects report no checksum and the journal layer hashes them
// on demand — the manifest pin stays the checksum source of truth.
package s3ws

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/BU-Neuromics/datapin/internal/workspace"
)

// Store implements workspace.Store over one bucket (+ optional prefix).
type Store struct {
	client *minio.Client
	bucket string
	prefix string // "" or "some/prefix" (no trailing slash)
}

// New connects per the s3://<endpoint>/<bucket>[/<prefix>] URL. token is
// "ACCESSKEY:SECRETKEY" ("" falls back to AWS env vars). Query params:
// insecure=true for plain HTTP (local MinIO), region=<r>.
func New(rawURL, token string) (*Store, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "s3" || u.Host == "" {
		return nil, fmt.Errorf("not an s3://endpoint/bucket URL: %q", rawURL)
	}
	parts := strings.SplitN(strings.Trim(u.Path, "/"), "/", 2)
	if parts[0] == "" {
		return nil, fmt.Errorf("s3 URL needs a bucket: s3://endpoint/bucket[/prefix]")
	}
	bucket := parts[0]
	prefix := ""
	if len(parts) == 2 {
		prefix = parts[1]
	}

	access, secret := os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY")
	if token != "" {
		if a, s, ok := strings.Cut(token, ":"); ok {
			access, secret = a, s
		} else {
			return nil, fmt.Errorf("an s3 remote's token must be \"ACCESSKEY:SECRETKEY\"")
		}
	}
	if access == "" || secret == "" {
		return nil, fmt.Errorf("no S3 credentials — store \"ACCESSKEY:SECRETKEY\" as the remote token or set AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY")
	}

	client, err := minio.New(u.Host, &minio.Options{
		Creds:  credentials.NewStaticV4(access, secret, ""),
		Secure: u.Query().Get("insecure") != "true",
		Region: u.Query().Get("region"),
	})
	if err != nil {
		return nil, err
	}
	return &Store{client: client, bucket: bucket, prefix: prefix}, nil
}

// NewFromClient wraps an existing client (tests).
func NewFromClient(client *minio.Client, bucket, prefix string) *Store {
	return &Store{client: client, bucket: bucket, prefix: prefix}
}

// Ping verifies the bucket is reachable.
func (s *Store) Ping(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("checking bucket %q: %w", s.bucket, err)
	}
	if !exists {
		return fmt.Errorf("bucket %q does not exist", s.bucket)
	}
	return nil
}

func (s *Store) object(key string) string {
	if s.prefix == "" {
		return key
	}
	return path.Join(s.prefix, key)
}

// etagMD5 returns the ETag as an MD5 when it is one (simple PUTs);
// multipart ETags carry a "-part" suffix and are not content hashes.
func etagMD5(etag string) string {
	etag = strings.Trim(etag, `"`)
	if len(etag) == 32 && !strings.Contains(etag, "-") {
		return strings.ToLower(etag)
	}
	return ""
}

// List implements workspace.Store.
func (s *Store) List(ctx context.Context) (map[string]workspace.ObjectInfo, error) {
	out := map[string]workspace.ObjectInfo{}
	base := s.prefix
	if base != "" {
		base += "/"
	}
	for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: base, Recursive: true}) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		key := strings.TrimPrefix(obj.Key, base)
		if key == "" || strings.HasPrefix(key, workspace.Prefix+"/") {
			continue
		}
		out[key] = workspace.ObjectInfo{Size: obj.Size, MD5: etagMD5(obj.ETag)}
	}
	return out, nil
}

// ListPrefix implements workspace.PrefixLister.
func (s *Store) ListPrefix(ctx context.Context, prefix string) ([]string, error) {
	full := s.object(prefix)
	var keys []string
	for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: full, Recursive: true}) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		key := obj.Key
		if s.prefix != "" {
			key = strings.TrimPrefix(key, s.prefix+"/")
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

// Stat implements workspace.Store.
func (s *Store) Stat(ctx context.Context, key string) (workspace.ObjectInfo, bool, error) {
	info, err := s.client.StatObject(ctx, s.bucket, s.object(key), minio.StatObjectOptions{})
	if err != nil {
		resp := minio.ToErrorResponse(err)
		if resp.Code == "NoSuchKey" || resp.StatusCode == 404 {
			return workspace.ObjectInfo{}, false, nil
		}
		return workspace.ObjectInfo{}, false, err
	}
	return workspace.ObjectInfo{Size: info.Size, MD5: etagMD5(info.ETag)}, true, nil
}

// Get implements workspace.Store.
func (s *Store) Get(ctx context.Context, key string, w io.Writer) error {
	obj, err := s.client.GetObject(ctx, s.bucket, s.object(key), minio.GetObjectOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = obj.Close() }()
	_, err = io.Copy(w, obj)
	return err
}

// Put implements workspace.Store.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	_, err := s.client.PutObject(ctx, s.bucket, s.object(key), r, size, minio.PutObjectOptions{})
	return err
}

// Copy implements workspace.Store via server-side CopyObject — no bytes
// transit through datapin (the D4 archive step is cheap on S3).
func (s *Store) Copy(ctx context.Context, src, dst string) error {
	_, err := s.client.CopyObject(ctx,
		minio.CopyDestOptions{Bucket: s.bucket, Object: s.object(dst)},
		minio.CopySrcOptions{Bucket: s.bucket, Object: s.object(src)},
	)
	return err
}

// Delete implements workspace.Store.
func (s *Store) Delete(ctx context.Context, key string) error {
	return s.client.RemoveObject(ctx, s.bucket, s.object(key), minio.RemoveObjectOptions{})
}
