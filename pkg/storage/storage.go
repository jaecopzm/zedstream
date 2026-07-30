package storage

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Client wraps the S3-compatible R2 client.
type Client struct {
	s3            *s3.Client
	presigner     *s3.PresignClient
	publicURLAudio  string
	publicURLImages string
}

// Config holds R2 connection parameters.
type Config struct {
	AccountID       string
	AccessKeyID     string
	SecretAccessKey string
	PublicURLAudio  string
	PublicURLImages string
}

// New creates a new Cloudflare R2 storage client.
func New(cfg Config) (*Client, error) {
	endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com", cfg.AccountID)

	r2Cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		),
		awsconfig.WithRegion("auto"),
	)
	if err != nil {
		return nil, fmt.Errorf("load r2 config: %w", err)
	}

	s3Client := s3.NewFromConfig(r2Cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})

	return &Client{
		s3:              s3Client,
		presigner:       s3.NewPresignClient(s3Client),
		publicURLAudio:  cfg.PublicURLAudio,
		publicURLImages: cfg.PublicURLImages,
	}, nil
}

// UploadFile uploads a file to the specified bucket with the given key.
func (c *Client) UploadFile(ctx context.Context, bucket, key, contentType string, body io.Reader) error {
	_, err := c.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		Body:        body,
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("upload file to r2: %w", err)
	}
	return nil
}

// GetSignedURL generates a pre-signed GET URL for the given object.
func (c *Client) GetSignedURL(ctx context.Context, bucket, key string, expiry time.Duration) (string, error) {
	req, err := c.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", fmt.Errorf("presign get object: %w", err)
	}
	return req.URL, nil
}

// DeleteFile removes an object from the specified bucket.
func (c *Client) DeleteFile(ctx context.Context, bucket, key string) error {
	_, err := c.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("delete file from r2: %w", err)
	}
	return nil
}

// PublicURL returns the public CDN URL for an image object.
func (c *Client) PublicURL(key string) string {
	return fmt.Sprintf("%s/%s", c.publicURLImages, key)
}

// PublicAudioURL returns the public CDN URL for an audio object.
func (c *Client) PublicAudioURL(key string) string {
	return fmt.Sprintf("%s/%s", c.publicURLAudio, key)
}
