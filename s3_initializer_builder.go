package lds3

import (
	"context"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/launchdarkly/go-server-sdk/v7/subsystems"
)

const (
	// DefaultMaxObjectSize is the default value for DataInitializerBuilder.MaxObjectSize: 100MB.
	DefaultMaxObjectSize int64 = 100 * 1024 * 1024

	// DefaultTimeout is the default value for DataInitializerBuilder.Timeout.
	DefaultTimeout = 30 * time.Second
)

// S3Client is the subset of the AWS S3 client's operations that the data initializer uses.
// It is implemented by *s3.Client, and can be implemented by test fakes.
type S3Client interface {
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// DuplicateKeysHandling is a parameter type used with DataInitializerBuilder.DuplicateKeysHandling.
//
// Deprecated: The initializer reads one object, so keys cannot be duplicated across objects.
type DuplicateKeysHandling string

const (
	// DuplicateKeysFail is an option for DataInitializerBuilder.DuplicateKeysHandling.
	//
	// Deprecated: The initializer reads one object, so this option has no effect.
	DuplicateKeysFail DuplicateKeysHandling = "fail"

	// DuplicateKeysKeepFirst is an option for DataInitializerBuilder.DuplicateKeysHandling.
	//
	// Deprecated: The initializer reads one object, so this option has no effect.
	DuplicateKeysKeepFirst DuplicateKeysHandling = "ignore"
)

// DataInitializerBuilder is a builder for configuring the S3 data initializer.
//
// Obtain an instance of this type by calling [DataInitializer]. After calling its methods to
// specify any desired custom settings, pass it to the Initializers method of
// [github.com/launchdarkly/go-server-sdk/v7/ldcomponents.DataSystemConfigurationBuilder].
//
// Builder calls can be chained, for example:
//
//	lds3.DataInitializer("my-bucket", "snapshot.json").AWSConfig(myConfig)
//
// You do not need to call the builder's Build method yourself; that will be done by the SDK.
type DataInitializerBuilder struct {
	bucket        string
	objectKeys    []string
	awsConfig     *aws.Config
	client        S3Client
	maxObjectSize int64
	timeout       time.Duration
}

// DataInitializerForObject returns a configurable builder for an S3 data initializer.
//
// The bucket is the name of the S3 bucket to read from, and objectKey is the key of an object
// within it, containing flag data in the SDK's file data source format.
func DataInitializerForObject(bucket string, objectKey string) *DataInitializerBuilder {
	return newDataInitializerBuilder(bucket, []string{objectKey})
}

// DataInitializer returns a configurable builder for an S3 data initializer.
//
// Deprecated: Use [DataInitializerForObject] instead. The initializer reads one object; when
// more than one object key is given, only the first is used.
func DataInitializer(bucket string, objectKeys ...string) *DataInitializerBuilder {
	return newDataInitializerBuilder(bucket, objectKeys)
}

func newDataInitializerBuilder(bucket string, objectKeys []string) *DataInitializerBuilder {
	return &DataInitializerBuilder{
		bucket:        bucket,
		objectKeys:    objectKeys,
		maxObjectSize: DefaultMaxObjectSize,
		timeout:       DefaultTimeout,
	}
}

// ObjectKeys adds keys of objects to read from the bucket, after any keys that were previously
// specified.
//
// Deprecated: The initializer reads one object; keys after the first are ignored.
func (b *DataInitializerBuilder) ObjectKeys(objectKeys ...string) *DataInitializerBuilder {
	b.objectKeys = append(b.objectKeys, objectKeys...)
	return b
}

// DuplicateKeysHandling specifies how to handle flag or segment keys that are duplicated across
// objects.
//
// Deprecated: The initializer reads one object, so this option has no effect.
func (b *DataInitializerBuilder) DuplicateKeysHandling(
	DuplicateKeysHandling,
) *DataInitializerBuilder {
	return b
}

// AWSConfig specifies an AWS configuration to create the S3 client from, instead of the default
// configuration sources (environment variables, shared config files, and instance metadata).
//
// This setting is ignored if a client is provided with [DataInitializerBuilder.S3Client].
func (b *DataInitializerBuilder) AWSConfig(config aws.Config) *DataInitializerBuilder {
	b.awsConfig = &config
	return b
}

// S3Client specifies an existing S3 client instance for the initializer to use, instead of
// creating one. Use this if you need full control of the client's configuration, for example to
// point it at an S3-compatible service.
func (b *DataInitializerBuilder) S3Client(client S3Client) *DataInitializerBuilder {
	b.client = client
	return b
}

// MaxObjectSize specifies the maximum size in bytes of a single object's flag data. The limit
// applies to each object separately, and to the decompressed content if an object is compressed.
// Reading stops with an error if an object exceeds it.
//
// The limit exists so that a misconfigured object key cannot make the SDK buffer an arbitrarily
// large download during startup. If this is not specified, the default is [DefaultMaxObjectSize].
// The value must be greater than zero.
func (b *DataInitializerBuilder) MaxObjectSize(limit int64) *DataInitializerBuilder {
	b.maxObjectSize = limit
	return b
}

// Timeout specifies a time limit for the entire data-fetching operation, including reading every
// configured object. If the limit is reached, the initializer fails and the SDK moves on to the
// next initializer or synchronizer, instead of letting a slow S3 request consume the SDK's whole
// initialization wait budget.
//
// If this is not specified, the default is [DefaultTimeout]. A value of zero disables the limit,
// leaving the operation bounded only by the SDK's initialization context. The value must not be
// negative.
func (b *DataInitializerBuilder) Timeout(timeout time.Duration) *DataInitializerBuilder {
	b.timeout = timeout
	return b
}

// Build creates a DataInitializer instance that can be used by the SDK client.
func (b *DataInitializerBuilder) Build(
	clientContext subsystems.ClientContext,
) (subsystems.DataInitializer, error) {
	if b.bucket == "" {
		return nil, errors.New("bucket name is required")
	}
	if len(b.objectKeys) == 0 {
		return nil, errors.New("at least one object key is required")
	}
	if b.maxObjectSize <= 0 {
		return nil, errors.New("maximum object size must be greater than zero")
	}
	if b.timeout < 0 {
		return nil, errors.New("timeout must not be negative")
	}
	return newS3DataInitializer(clientContext, b), nil
}
