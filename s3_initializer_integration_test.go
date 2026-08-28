package lds3

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/launchdarkly/go-server-sdk/v7/subsystems"

	"github.com/stretchr/testify/assert"
)

// These tests exercise the initializer through a real *s3.Client against a local HTTP server.
// They cover the wire path that the fake client cannot: endpoint resolution, request signing,
// and response and error deserialization.

const integrationBucket = "test-bucket"

// startS3Server starts an HTTP server that answers path-style GetObject requests for one bucket.
func startS3Server(t *testing.T, objects map[string]fakeObject) *httptest.Server {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		key, inBucket := strings.CutPrefix(path, integrationBucket+"/")
		var obj fakeObject
		found := false
		if inBucket {
			obj, found = objects[key]
		}
		if !found {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>` +
				`<Error><Code>NoSuchKey</Code><Message>The specified key does not exist.</Message></Error>`))
			return
		}
		if obj.contentEncoding != "" {
			w.Header().Set("Content-Encoding", obj.contentEncoding)
		}
		_, _ = w.Write(obj.data)
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func testCredentials() aws.CredentialsProvider {
	return credentials.NewStaticCredentialsProvider("test-access-key", "test-secret-key", "")
}

func realS3Client(server *httptest.Server) *s3.Client {
	return s3.New(s3.Options{
		BaseEndpoint: aws.String(server.URL),
		UsePathStyle: true,
		Region:       "us-east-1",
		Credentials:  testCredentials(),
	})
}

func TestRealClientLoadsFlags(t *testing.T) {
	server := startS3Server(t, simpleObjects(map[string][]byte{
		"flags.json": []byte(`{"flagValues": {"real-flag": true}, "segments": {"seg1": {"key": "seg1"}}}`),
	}))
	initializer := buildInitializer(t,
		DataInitializer(integrationBucket, "flags.json").S3Client(realS3Client(server)))
	basis := fetchBasis(t, initializer)

	assert.Equal(t, []string{"real-flag"}, itemKeys(collectionByKind(t, basis, "features")))
	assert.Equal(t, []string{"seg1"}, itemKeys(collectionByKind(t, basis, "segments")))
}

func TestRealClientReturnsTypedErrorForMissingObject(t *testing.T) {
	server := startS3Server(t, nil)
	err := fetchError(t,
		DataInitializer(integrationBucket, "missing.json").S3Client(realS3Client(server)))

	assert.Contains(t, err.Error(), "missing.json")
	var noSuchKey *types.NoSuchKey
	assert.ErrorAs(t, err, &noSuchKey,
		"the wrapped error should still match the typed AWS error")
}

func TestAWSConfigCredentialFailureSurfaces(t *testing.T) {
	server := startS3Server(t, simpleObjects(map[string][]byte{
		"flags.json": []byte(`{"flagValues": {"cfg-flag": 1}}`),
	}))
	config := aws.Config{
		Region: "us-east-1",
		Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{}, errors.New("credential source is unavailable")
		}),
		BaseEndpoint: aws.String(server.URL),
	}
	err := fetchError(t, DataInitializer(integrationBucket, "flags.json").AWSConfig(config))
	assert.Contains(t, err.Error(), "credential source is unavailable")
}

// Interface satisfaction check: the real client must implement S3Client.
var _ S3Client = (*s3.Client)(nil)

// Compile-time check that the initializer is usable where the SDK expects a component configurer.
var _ subsystems.ComponentConfigurer[subsystems.DataInitializer] = (*DataInitializerBuilder)(nil)
