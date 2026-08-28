package lds3

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/launchdarkly/go-server-sdk/v7/subsystems"
)

// S3Client is the subset of the AWS S3 client's operations that the data initializer uses.
// It is implemented by *s3.Client, and can be implemented by test fakes.
type S3Client interface {
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// DuplicateKeysHandling is a parameter type used with DataInitializerBuilder.DuplicateKeysHandling.
type DuplicateKeysHandling string

const (
	// DuplicateKeysFail is an option for DataInitializerBuilder.DuplicateKeysHandling, meaning that
	// data loading should fail if keys are duplicated across objects. This is the default behavior.
	DuplicateKeysFail DuplicateKeysHandling = "fail"

	// DuplicateKeysKeepFirst is an option for DataInitializerBuilder.DuplicateKeysHandling,
	// meaning that if keys are duplicated across objects the first occurrence will be used, in the
	// order the object keys were given.
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
	bucket                string
	objectKeys            []string
	duplicateKeysHandling DuplicateKeysHandling
	awsConfig             *aws.Config
	client                S3Client
}

// DataInitializer returns a configurable builder for an S3 data initializer.
//
// The bucket is the name of the S3 bucket to read from, and objectKeys are the keys of one or
// more objects within it, each containing flag data in the SDK's file data source format. At
// least one object key is required; additional keys may also be added with
// [DataInitializerBuilder.ObjectKeys].
func DataInitializer(bucket string, objectKeys ...string) *DataInitializerBuilder {
	return &DataInitializerBuilder{
		bucket:                bucket,
		objectKeys:            objectKeys,
		duplicateKeysHandling: DuplicateKeysFail,
	}
}

// ObjectKeys adds keys of objects to read from the bucket, after any keys that were previously
// specified.
func (b *DataInitializerBuilder) ObjectKeys(objectKeys ...string) *DataInitializerBuilder {
	b.objectKeys = append(b.objectKeys, objectKeys...)
	return b
}

// DuplicateKeysHandling specifies how to handle flag or segment keys that are duplicated across
// objects.
//
// If this is not specified, or if you set it to an unrecognized value, the default is
// DuplicateKeysFail.
func (b *DataInitializerBuilder) DuplicateKeysHandling(
	duplicateKeysHandling DuplicateKeysHandling,
) *DataInitializerBuilder {
	b.duplicateKeysHandling = duplicateKeysHandling
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
	return newS3DataInitializer(clientContext, b), nil
}
