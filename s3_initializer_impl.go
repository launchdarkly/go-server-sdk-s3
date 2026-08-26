package lds3

import (
	"context"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/launchdarkly/go-sdk-common/v3/ldlog"
	"github.com/launchdarkly/go-server-sdk/v7/subsystems"
	"github.com/launchdarkly/go-server-sdk/v7/subsystems/ldstoreimpl"
	"github.com/launchdarkly/go-server-sdk/v7/subsystems/ldstoretypes"
)

type s3DataInitializer struct {
	bucket                string
	objectKeys            []string
	duplicateKeysHandling DuplicateKeysHandling
	awsConfig             *aws.Config
	client                S3Client
	loggers               ldlog.Loggers
}

func newS3DataInitializer(
	context subsystems.ClientContext,
	builder *DataInitializerBuilder,
) *s3DataInitializer {
	loggers := context.GetLogging().Loggers
	loggers.SetPrefix("S3DataInitializer:")
	return &s3DataInitializer{
		bucket:                builder.bucket,
		objectKeys:            builder.objectKeys,
		duplicateKeysHandling: builder.duplicateKeysHandling,
		awsConfig:             builder.awsConfig,
		client:                builder.client,
		loggers:               loggers,
	}
}

func (s *s3DataInitializer) Name() string {
	return "S3DataInitializer"
}

func (s *s3DataInitializer) Fetch(
	_ subsystems.DataSelector,
	ctx context.Context,
) (*subsystems.Basis, bool, error) {
	client := s.client
	if client == nil {
		config := s.awsConfig
		if config == nil {
			loaded, err := awsconfig.LoadDefaultConfig(ctx)
			if err != nil {
				return nil, false, fmt.Errorf("unable to load AWS configuration: %w", err)
			}
			config = &loaded
		}
		client = s3.NewFromConfig(*config)
	}

	docs := make([]document, 0, len(s.objectKeys))
	for _, objectKey := range s.objectKeys {
		doc, err := s.readObject(ctx, client, objectKey)
		if err != nil {
			return nil, false, err
		}
		docs = append(docs, doc)
	}
	merged, err := merge(s.duplicateKeysHandling, docs...)
	if err != nil {
		return nil, false, err
	}
	if len(merged.Flags) == 0 && len(merged.Segments) == 0 {
		// A snapshot with zero items is far more likely to be a corrupted or misproduced
		// object than an intentional configuration, and applying it would silently turn
		// off every flag. Refuse it so the SDK can fall back to another data source.
		return nil, false, fmt.Errorf(
			"object(s) in S3 bucket %q contained no flag or segment data; refusing to initialize from an empty snapshot",
			s.bucket)
	}
	changeSet, err := s.makeChangeSet(merged)
	if err != nil {
		return nil, false, err
	}
	s.loggers.Infof("Loaded %d flag(s) and %d segment(s) from %d object(s) in S3 bucket %s",
		len(merged.Flags), len(merged.Segments), len(s.objectKeys), s.bucket)
	return &subsystems.Basis{
		ChangeSet: *changeSet,
		Persist:   false,
	}, false, nil
}

func (s *s3DataInitializer) readObject(
	ctx context.Context,
	client S3Client,
	objectKey string,
) (document, error) {
	output, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(objectKey),
	})
	if err != nil {
		return document{}, fmt.Errorf("unable to get object %q from S3 bucket %q: %w",
			objectKey, s.bucket, err)
	}
	defer output.Body.Close() //nolint:errcheck
	rawData, err := io.ReadAll(output.Body)
	if err != nil {
		return document{}, fmt.Errorf("unable to read object %q from S3 bucket %q: %w",
			objectKey, s.bucket, err)
	}
	if !utf8.Valid(rawData) {
		return document{}, fmt.Errorf("object %q in S3 bucket %q is not valid UTF-8",
			objectKey, s.bucket)
	}
	doc, err := parseDocument(rawData)
	if err != nil {
		return document{}, fmt.Errorf("error parsing object %q from S3 bucket %q: %w",
			objectKey, s.bucket, err)
	}
	if err := validateDocument(doc); err != nil {
		return document{}, fmt.Errorf("invalid data in object %q from S3 bucket %q: %w",
			objectKey, s.bucket, err)
	}
	return doc, nil
}

// validateDocument applies checks that the shared file-data parsing does not: an item's embedded
// key must match the key it is defined under, and versions must not be negative. Both indicate a
// malformed or corrupted document that would misbehave later in evaluations or events.
func validateDocument(doc document) error {
	if doc.Flags != nil {
		for key, f := range *doc.Flags {
			if f.Key != key {
				return fmt.Errorf("flag %q has a mismatched embedded key %q", key, f.Key)
			}
			if f.Version < 0 {
				return fmt.Errorf("flag %q has a negative version %d", key, f.Version)
			}
		}
	}
	if doc.Segments != nil {
		for key, seg := range *doc.Segments {
			if seg.Key != key {
				return fmt.Errorf("segment %q has a mismatched embedded key %q", key, seg.Key)
			}
			if seg.Version < 0 {
				return fmt.Errorf("segment %q has a negative version %d", key, seg.Version)
			}
		}
	}
	return nil
}

// makeChangeSet expresses a merged data set as a full-transfer change set.
func (s *s3DataInitializer) makeChangeSet(merged mergeResult) (*subsystems.ChangeSet, error) {
	intent := subsystems.ServerIntent{
		Payload: subsystems.Payload{
			ID:     "",
			Target: 1,
			Code:   subsystems.IntentTransferFull,
			Reason: "payload-missing",
		},
	}

	collections := make([]ldstoretypes.Collection, 0, 2)
	if len(merged.Flags) > 0 {
		collections = append(collections, ldstoretypes.Collection{
			Kind:  ldstoreimpl.Features(),
			Items: merged.Flags,
		})
	}
	if len(merged.Segments) > 0 {
		collections = append(collections, ldstoretypes.Collection{
			Kind:  ldstoreimpl.Segments(),
			Items: merged.Segments,
		})
	}

	// S3 objects are a static snapshot with no notion of payload state, so there is no selector;
	// this data cannot serve as the basis for delta updates from another synchronizer.
	return subsystems.NewChangeSetFromCollections(intent, subsystems.NoSelector(), collections)
}
