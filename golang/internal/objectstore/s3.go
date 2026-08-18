package objectstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/rachlenko/parparchik/golang/internal/config"
)

// S3Store implements Store against any S3-compatible endpoint (AWS S3 or
// MinIO), using aws-sdk-go-v2 for request signing instead of a hand-rolled
// SigV4 implementation.
type S3Store struct {
	client           *s3.Client
	presign          *s3.PresignClient
	region           string
	externalEndpoint string
}

// NewS3Store builds an S3Store from parparchik configuration. When
// cfg.S3Endpoint is set, requests target that endpoint path-style (the MinIO
// case); otherwise the SDK's default AWS endpoint resolution for cfg.Region
// applies.
func NewS3Store(ctx context.Context, cfg *config.Config) (*S3Store, error) {
	loadOpts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.AWSRegion),
	}
	if cfg.AWSAccessKeyID != "" && cfg.AWSSecretAccessKey != "" {
		loadOpts = append(loadOpts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AWSAccessKeyID, cfg.AWSSecretAccessKey, ""),
		))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("objectstore: load AWS config: %w", err)
	}

	// Resolved once here (not per-request in PublicURL) so a bare
	// "host:port" only logs its http:// assumption warning once, and so
	// PublicURL never has to re-parse or re-guess the scheme.
	external := cfg.S3ExternalEndpoint
	if external == "" {
		external = cfg.S3Endpoint
	}
	var externalURL string
	if external != "" {
		externalURL = resolveEndpointURL(external)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.S3Endpoint != "" {
			o.BaseEndpoint = aws.String(resolveEndpointURL(cfg.S3Endpoint))
			o.UsePathStyle = true
		}
	})

	// Presigned URLs must be signed against — and therefore must use —
	// whatever host the *client that follows the redirect* will actually
	// connect to, not necessarily the same host this process uses to talk
	// to storage directly. In the common docker-compose shape,
	// S3_ENDPOINT=minio:9000 (only resolvable inside the Docker network)
	// while S3_EXTERNAL_ENDPOINT=localhost:9000 (what a client outside the
	// network must use); reusing the internal client to presign would
	// produce a URL an external client can't even resolve. When no custom
	// endpoint is configured at all (real AWS S3), presigning against the
	// default client is correct as-is — there's nothing to redirect away
	// from.
	presignClient := client
	if cfg.S3Endpoint != "" {
		presignClient = s3.NewFromConfig(awsCfg, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(externalURL)
			o.UsePathStyle = true
		})
	}

	return &S3Store{
		client:           client,
		presign:          s3.NewPresignClient(presignClient),
		region:           cfg.AWSRegion,
		externalEndpoint: externalURL,
	}, nil
}

func (s *S3Store) ListObjects(ctx context.Context, bucket string) ([]Object, error) {
	var result []Object

	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("objectstore: list objects in %q: %w", bucket, err)
		}
		for _, obj := range page.Contents {
			result = append(result, Object{
				Key:          aws.ToString(obj.Key),
				Size:         aws.ToInt64(obj.Size),
				LastModified: formatTime(obj.LastModified),
			})
		}
	}

	return result, nil
}

func (s *S3Store) HeadObject(ctx context.Context, bucket, key string) (*Object, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("objectstore: head %q/%q: %w", bucket, key, err)
	}

	return &Object{
		Key:          key,
		Size:         aws.ToInt64(out.ContentLength),
		LastModified: formatTime(out.LastModified),
	}, nil
}

func (s *S3Store) GetObject(ctx context.Context, bucket, key string) ([]byte, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("objectstore: get %q/%q: %w", bucket, key, err)
	}
	defer out.Body.Close()

	body, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("objectstore: read %q/%q body: %w", bucket, key, err)
	}
	return body, nil
}

func (s *S3Store) PutObject(ctx context.Context, bucket, key string, content []byte, contentType string) error {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(content),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("objectstore: put %q/%q: %w", bucket, key, err)
	}
	return nil
}

func (s *S3Store) PublicURL(bucket, key string) string {
	if s.externalEndpoint != "" {
		// externalEndpoint is already a full scheme://host URL (resolved
		// once in NewS3Store via resolveEndpointURL) — do not re-prepend a
		// scheme here, or a caller-supplied "https://cdn.example.com"
		// becomes the malformed "http://https://cdn.example.com/...".
		return s.externalEndpoint + "/" + bucket + "/" + key
	}
	return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", bucket, s.region, key)
}

func (s *S3Store) PresignedURL(ctx context.Context, bucket, key string, expires time.Duration) (string, error) {
	req, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(expires))
	if err != nil {
		return "", fmt.Errorf("objectstore: presign %q/%q: %w", bucket, key, err)
	}
	return req.URL, nil
}

// resolveEndpointURL accepts either a bare "host:port" (assumed http://, for
// local/internal deployments like the docker-compose MinIO stack — same
// assumption the Lua original hardcoded) or a full "scheme://host:port"
// URL, in which case the caller's scheme (e.g. https://) is honored as-is.
// The Lua original always hardcoded http:// with no way to opt into TLS for
// the internal S3 endpoint at all — see the nginx-lua security review's
// MEDIUM finding on this.
func resolveEndpointURL(endpoint string) string {
	if strings.Contains(endpoint, "://") {
		return endpoint
	}
	slog.Warn("objectstore: S3_ENDPOINT has no scheme, assuming plaintext http:// — set a full https:// URL for any non-local deployment", "endpoint", endpoint)
	return "http://" + endpoint
}

func isNotFound(err error) bool {
	var nf *types.NotFound
	var nsk *types.NoSuchKey
	return errors.As(err, &nf) || errors.As(err, &nsk)
}

func formatTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
