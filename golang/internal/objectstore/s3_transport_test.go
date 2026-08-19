package objectstore

// Tests in this file exercise S3Store's actual HTTP call sites
// (ListObjects/HeadObject/GetObject/PutObject) against a real HTTP server
// serving hand-built S3 API responses, rather than only the pure helpers
// (resolveEndpointURL, PublicURL, PresignedURL) the rest of this package's
// tests cover. This closes the gap flagged in docs/plans/ Task 11.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/rachlenko/parparchik/golang/internal/config"
)

func newTestStoreAgainst(t *testing.T, srv *httptest.Server) *S3Store {
	t.Helper()
	store, err := NewS3Store(context.Background(), &config.Config{
		AWSRegion:          "us-east-1",
		S3Endpoint:         srv.URL, // already scheme-qualified (http://127.0.0.1:port)
		AWSAccessKeyID:     "test-access-key",
		AWSSecretAccessKey: "test-secret-key",
	})
	if err != nil {
		t.Fatalf("NewS3Store() error = %v", err)
	}
	return store
}

// newNoRetryTestStoreAgainst builds a store like newTestStoreAgainst, but
// with retries disabled — for tests that deliberately provoke a persistent
// error, where the SDK's default retryer would otherwise spend several
// real seconds backing off and retrying against a server that will never
// succeed. NewS3Store itself has no retry-config knob (production code has
// no reason to want one), so this constructs the client directly; same
// package, so it can reach S3Store's unexported fields.
func newNoRetryTestStoreAgainst(t *testing.T, srv *httptest.Server) *S3Store {
	t.Helper()
	ctx := context.Background()
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test-access-key", "test-secret-key", "")),
	)
	if err != nil {
		t.Fatalf("LoadDefaultConfig() error = %v", err)
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = &srv.URL
		o.UsePathStyle = true
		o.RetryMaxAttempts = 1
	})
	return &S3Store{client: client, presign: s3.NewPresignClient(client), region: "us-east-1"}
}

func TestS3Store_HeadObject_Found(t *testing.T) {
	// Arrange
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("method = %s, want HEAD", r.Method)
		}
		w.Header().Set("Content-Length", "1234")
		w.Header().Set("Last-Modified", "Wed, 21 Oct 2026 07:28:00 GMT")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	store := newTestStoreAgainst(t, srv)

	// Act
	obj, err := store.HeadObject(context.Background(), "my-bucket", "my-key")

	// Assert
	if err != nil {
		t.Fatalf("HeadObject() error = %v", err)
	}
	if obj == nil || obj.Key != "my-key" || obj.Size != 1234 {
		t.Errorf("HeadObject() = %+v, want Key=my-key Size=1234", obj)
	}
}

func TestS3Store_HeadObject_NotFound(t *testing.T) {
	// Arrange: real S3 HeadObject 404s carry no body (HEAD responses can't
	// have one) — the SDK derives *types.NotFound purely from status code.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	store := newTestStoreAgainst(t, srv)

	// Act
	obj, err := store.HeadObject(context.Background(), "my-bucket", "missing-key")

	// Assert
	if err != nil {
		t.Fatalf("HeadObject() error = %v, want nil for a 404", err)
	}
	if obj != nil {
		t.Errorf("HeadObject() = %+v, want nil", obj)
	}
}

func TestS3Store_HeadObject_TransportFailureIsAnError(t *testing.T) {
	// Arrange: a non-404 failure must surface as an error, not "not found".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	store := newNoRetryTestStoreAgainst(t, srv)

	// Act
	obj, err := store.HeadObject(context.Background(), "my-bucket", "my-key")

	// Assert
	if err == nil {
		t.Fatal("HeadObject() error = nil, want an error for a 500")
	}
	if obj != nil {
		t.Errorf("HeadObject() = %+v, want nil on error", obj)
	}
}

func TestS3Store_GetObject_Found(t *testing.T) {
	// Arrange
	const content = "hello from S3"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		_, _ = io.WriteString(w, content)
	}))
	defer srv.Close()

	store := newTestStoreAgainst(t, srv)

	// Act
	body, err := store.GetObject(context.Background(), "my-bucket", "my-key")

	// Assert
	if err != nil {
		t.Fatalf("GetObject() error = %v", err)
	}
	if string(body) != content {
		t.Errorf("GetObject() = %q, want %q", body, content)
	}
}

func TestS3Store_GetObject_NotFound(t *testing.T) {
	// Arrange: unlike HEAD, GET 404s carry an XML error body the SDK
	// deserializes into *types.NoSuchKey.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>
<Error><Code>NoSuchKey</Code><Message>The specified key does not exist.</Message><Key>missing-key</Key><RequestId>1</RequestId></Error>`)
	}))
	defer srv.Close()

	store := newTestStoreAgainst(t, srv)

	// Act
	body, err := store.GetObject(context.Background(), "my-bucket", "missing-key")

	// Assert
	if err != nil {
		t.Fatalf("GetObject() error = %v, want nil for a NoSuchKey 404", err)
	}
	if body != nil {
		t.Errorf("GetObject() = %q, want nil", body)
	}
}

func TestS3Store_PutObject(t *testing.T) {
	// Arrange
	var gotMethod, gotContentType string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	store := newTestStoreAgainst(t, srv)

	// Act
	err := store.PutObject(context.Background(), "my-bucket", "my-key", []byte("payload"), "text/plain")

	// Assert
	if err != nil {
		t.Fatalf("PutObject() error = %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %s, want PUT", gotMethod)
	}
	if gotContentType != "text/plain" {
		t.Errorf("Content-Type = %q, want text/plain", gotContentType)
	}
	if string(gotBody) != "payload" {
		t.Errorf("body = %q, want payload", gotBody)
	}
}

func TestS3Store_PutObject_DefaultsContentType(t *testing.T) {
	// Arrange
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	store := newTestStoreAgainst(t, srv)

	// Act
	err := store.PutObject(context.Background(), "my-bucket", "my-key", []byte("payload"), "")

	// Assert
	if err != nil {
		t.Fatalf("PutObject() error = %v", err)
	}
	if gotContentType != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", gotContentType)
	}
}

func TestS3Store_PutObject_Error(t *testing.T) {
	// Arrange
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	store := newTestStoreAgainst(t, srv)

	// Act
	err := store.PutObject(context.Background(), "my-bucket", "my-key", []byte("payload"), "text/plain")

	// Assert
	if err == nil {
		t.Fatal("PutObject() error = nil, want an error for a 403")
	}
}

func TestS3Store_ListObjects_SinglePage(t *testing.T) {
	// Arrange
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Name>my-bucket</Name>
  <IsTruncated>false</IsTruncated>
  <Contents>
    <Key>a.txt</Key>
    <LastModified>2026-05-05T10:00:00.000Z</LastModified>
    <Size>10</Size>
  </Contents>
  <Contents>
    <Key>b.txt</Key>
    <LastModified>2026-05-06T10:00:00.000Z</LastModified>
    <Size>20</Size>
  </Contents>
</ListBucketResult>`)
	}))
	defer srv.Close()

	store := newTestStoreAgainst(t, srv)

	// Act
	objects, err := store.ListObjects(context.Background(), "my-bucket")

	// Assert
	if err != nil {
		t.Fatalf("ListObjects() error = %v", err)
	}
	if len(objects) != 2 {
		t.Fatalf("len(objects) = %d, want 2 (%+v)", len(objects), objects)
	}
	if objects[0].Key != "a.txt" || objects[0].Size != 10 {
		t.Errorf("objects[0] = %+v", objects[0])
	}
	if objects[1].Key != "b.txt" || objects[1].Size != 20 {
		t.Errorf("objects[1] = %+v", objects[1])
	}
}

func TestS3Store_ListObjects_FollowsPagination(t *testing.T) {
	// Arrange: two pages, joined by a continuation token.
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/xml")
		if r.URL.Query().Get("continuation-token") == "" {
			_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Name>my-bucket</Name>
  <IsTruncated>true</IsTruncated>
  <NextContinuationToken>page2</NextContinuationToken>
  <Contents><Key>a.txt</Key><LastModified>2026-05-05T10:00:00.000Z</LastModified><Size>10</Size></Contents>
</ListBucketResult>`)
			return
		}
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Name>my-bucket</Name>
  <IsTruncated>false</IsTruncated>
  <Contents><Key>b.txt</Key><LastModified>2026-05-06T10:00:00.000Z</LastModified><Size>20</Size></Contents>
</ListBucketResult>`)
	}))
	defer srv.Close()

	store := newTestStoreAgainst(t, srv)

	// Act
	objects, err := store.ListObjects(context.Background(), "my-bucket")

	// Assert
	if err != nil {
		t.Fatalf("ListObjects() error = %v", err)
	}
	if requests != 2 {
		t.Errorf("requests = %d, want 2 (pagination should follow the continuation token)", requests)
	}
	if len(objects) != 2 {
		t.Fatalf("len(objects) = %d, want 2 (%+v)", len(objects), objects)
	}
	keys := []string{objects[0].Key, objects[1].Key}
	if keys[0] != "a.txt" || keys[1] != "b.txt" {
		t.Errorf("keys = %v, want [a.txt b.txt]", keys)
	}
}

func TestS3Store_ListObjects_Empty(t *testing.T) {
	// Arrange
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Name>my-bucket</Name>
  <IsTruncated>false</IsTruncated>
</ListBucketResult>`)
	}))
	defer srv.Close()

	store := newTestStoreAgainst(t, srv)

	// Act
	objects, err := store.ListObjects(context.Background(), "my-bucket")

	// Assert
	if err != nil {
		t.Fatalf("ListObjects() error = %v", err)
	}
	if len(objects) != 0 {
		t.Errorf("len(objects) = %d, want 0", len(objects))
	}
}

func TestS3Store_ListObjects_TransportError(t *testing.T) {
	// Arrange
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	store := newNoRetryTestStoreAgainst(t, srv)

	// Act
	_, err := store.ListObjects(context.Background(), "my-bucket")

	// Assert
	if err == nil {
		t.Fatal("ListObjects() error = nil, want an error for a 500")
	}
}

// TestS3Store_RoundTrip exercises Put then Get then Head against a single
// in-memory fake bucket, as a sanity check that the three call sites agree
// on the same wire format rather than only being individually mocked.
func TestS3Store_RoundTrip(t *testing.T) {
	// Arrange: a minimal but stateful fake S3 bucket.
	objects := map[string][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path
		switch r.Method {
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			objects[key] = body
			w.WriteHeader(http.StatusOK)
		case http.MethodHead, http.MethodGet:
			body, ok := objects[key]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
			if r.Method == http.MethodGet {
				_, _ = w.Write(body)
			}
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	store := newTestStoreAgainst(t, srv)
	ctx := context.Background()

	// Act + Assert: not-found before put.
	if obj, err := store.HeadObject(ctx, "b", "roundtrip.txt"); err != nil || obj != nil {
		t.Fatalf("HeadObject() before Put = %+v, %v, want nil, nil", obj, err)
	}

	if err := store.PutObject(ctx, "b", "roundtrip.txt", []byte("round-trip-content"), "text/plain"); err != nil {
		t.Fatalf("PutObject() error = %v", err)
	}

	obj, err := store.HeadObject(ctx, "b", "roundtrip.txt")
	if err != nil || obj == nil || obj.Size != int64(len("round-trip-content")) {
		t.Fatalf("HeadObject() after Put = %+v, %v", obj, err)
	}

	body, err := store.GetObject(ctx, "b", "roundtrip.txt")
	if err != nil || string(body) != "round-trip-content" {
		t.Fatalf("GetObject() after Put = %q, %v", body, err)
	}
}
