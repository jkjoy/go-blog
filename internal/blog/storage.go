package blog

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type objectUploader func(context.Context, S3Settings, string, string, io.Reader, int64) error

func uploadS3Object(ctx context.Context, settings S3Settings, key, contentType string, body io.Reader, size int64) error {
	config, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(settings.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(settings.AccessKey, settings.SecretKey, "")),
	)
	if err != nil {
		return err
	}
	client := s3.NewFromConfig(config, func(options *s3.Options) {
		options.UsePathStyle = settings.PathStyle
		options.BaseEndpoint = aws.String(settings.Endpoint)
	})
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(settings.Bucket), Key: aws.String(key), Body: body,
		ContentLength: aws.Int64(size), ContentType: aws.String(contentType),
	})
	return err
}

func (settings S3Settings) objectURL(key string) string {
	base := strings.TrimRight(settings.PublicURL, "/")
	segments := strings.Split(key, "/")
	for index := range segments {
		segments[index] = url.PathEscape(segments[index])
	}
	return base + "/" + strings.Join(segments, "/")
}

func normalizeS3Prefix(value string) (string, error) {
	value = strings.Trim(strings.TrimSpace(value), "/")
	if value == "" {
		return "", nil
	}
	if strings.Contains(value, "\\") || path.Clean(value) != value || strings.HasPrefix(value, "..") {
		return "", fmt.Errorf("S3 路径前缀不合法")
	}
	return value, nil
}
