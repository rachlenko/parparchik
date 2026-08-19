package resolver

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/rachlenko/parparchik/golang/internal/objectstore"
)

// fakeObject is one object stored in a fakeStore bucket.
type fakeObject struct {
	content      []byte
	lastModified string
}

// fakeStore is an in-memory objectstore.Store used only by this package's
// tests, so resolver logic can be exercised without a real S3/MinIO
// endpoint.
type fakeStore struct {
	mu      sync.Mutex
	buckets map[string]map[string]fakeObject
}

func newFakeStore() *fakeStore {
	return &fakeStore{buckets: make(map[string]map[string]fakeObject)}
}

func (s *fakeStore) put(bucket, key string, content []byte, lastModified string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.buckets[bucket] == nil {
		s.buckets[bucket] = make(map[string]fakeObject)
	}
	s.buckets[bucket][key] = fakeObject{content: content, lastModified: lastModified}
}

func (s *fakeStore) ListObjects(_ context.Context, bucket string) ([]objectstore.Object, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []objectstore.Object
	for key, obj := range s.buckets[bucket] {
		result = append(result, objectstore.Object{Key: key, Size: int64(len(obj.content)), LastModified: obj.lastModified})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result, nil
}

func (s *fakeStore) HeadObject(_ context.Context, bucket, key string) (*objectstore.Object, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.buckets[bucket][key]
	if !ok {
		return nil, nil
	}
	return &objectstore.Object{Key: key, Size: int64(len(obj.content)), LastModified: obj.lastModified}, nil
}

func (s *fakeStore) GetObject(_ context.Context, bucket, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.buckets[bucket][key]
	if !ok {
		return nil, nil
	}
	return obj.content, nil
}

func (s *fakeStore) PutObject(_ context.Context, bucket, key string, content []byte, _ string) error {
	s.put(bucket, key, content, "")
	return nil
}

func (s *fakeStore) PublicURL(bucket, key string) string {
	return fmt.Sprintf("http://public.example/%s/%s", bucket, key)
}

func (s *fakeStore) PresignedURL(_ context.Context, bucket, key string, expires time.Duration) (string, error) {
	return fmt.Sprintf("http://presigned.example/%s/%s?expires=%s", bucket, key, expires), nil
}
