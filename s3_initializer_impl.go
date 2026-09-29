package lds3

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/launchdarkly/go-sdk-common/v3/ldlog"
	"github.com/launchdarkly/go-server-sdk/v7/subsystems"
	"github.com/launchdarkly/go-server-sdk/v7/subsystems/ldstoreimpl"
	"github.com/launchdarkly/go-server-sdk/v7/subsystems/ldstoretypes"

	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
)

var gzipMagic = []byte{0x1f, 0x8b} //nolint:gochecknoglobals

var errObjectTooLarge = errors.New("object content exceeds the size limit")

type s3DataInitializer struct {
	bucket                string
	objectKeys            []string
	duplicateKeysHandling DuplicateKeysHandling
	awsConfig             *aws.Config
	maxObjectSize         int64
	timeout               time.Duration
	loggers               ldlog.Loggers

	clientMu sync.Mutex
	client   S3Client
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
		maxObjectSize:         builder.maxObjectSize,
		timeout:               builder.timeout,
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
	if s.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}

	client, err := s.getClient(ctx)
	if err != nil {
		return nil, false, err
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

// getClient returns the configured S3 client, creating one on the first call if the builder did
// not supply one. The created client is kept for any later Fetch calls.
func (s *s3DataInitializer) getClient(ctx context.Context) (S3Client, error) {
	s.clientMu.Lock()
	defer s.clientMu.Unlock()
	if s.client != nil {
		return s.client, nil
	}
	config := s.awsConfig
	if config == nil {
		loaded, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			return nil, fmt.Errorf("unable to load AWS configuration: %w", err)
		}
		config = &loaded
	}
	s.client = s3.NewFromConfig(*config)
	return s.client, nil
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
		return document{}, s.describeGetObjectError(objectKey, err)
	}
	defer output.Body.Close() //nolint:errcheck

	if output.ContentLength != nil && *output.ContentLength > s.maxObjectSize {
		return document{}, fmt.Errorf(
			"object %q in S3 bucket %q is %d bytes, which exceeds the %d-byte limit (the MaxObjectSize option can raise it)",
			objectKey, s.bucket, *output.ContentLength, s.maxObjectSize)
	}

	var reader io.Reader = output.Body
	isGzip := output.ContentEncoding != nil && strings.EqualFold(*output.ContentEncoding, "gzip")
	if isGzip {
		gz, err := gzip.NewReader(reader)
		if err != nil {
			return document{}, fmt.Errorf("unable to decompress object %q from S3 bucket %q: %w",
				objectKey, s.bucket, err)
		}
		defer gz.Close() //nolint:errcheck
		reader = gz
	}

	rawData, err := readLimited(reader, s.maxObjectSize)
	if err != nil {
		if errors.Is(err, errObjectTooLarge) {
			return document{}, fmt.Errorf(
				"content of object %q in S3 bucket %q exceeds the %d-byte limit (the MaxObjectSize option can raise it)",
				objectKey, s.bucket, s.maxObjectSize)
		}
		return document{}, fmt.Errorf("unable to read object %q from S3 bucket %q: %w",
			objectKey, s.bucket, err)
	}

	if !isGzip && bytes.HasPrefix(rawData, gzipMagic) {
		return document{}, fmt.Errorf(
			"object %q in S3 bucket %q appears to be gzip-compressed, but its Content-Encoding is not set to gzip",
			objectKey, s.bucket)
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
	if doc.FlagValues != nil {
		// The file data sources' simplified flag-key-to-value entries are not supported: this
		// initializer consumes snapshots of real flag data, which express full definitions.
		// Rejecting the section, rather than ignoring it as an unknown property, prevents a
		// file written for the file data sources from silently losing flags.
		return document{}, fmt.Errorf(
			`object %q in S3 bucket %q contains a "flagValues" section, which is not supported; snapshots must contain full flag definitions`,
			objectKey, s.bucket)
	}
	if err := validateDocument(doc); err != nil {
		return document{}, fmt.Errorf("invalid data in object %q from S3 bucket %q: %w",
			objectKey, s.bucket, err)
	}
	return doc, nil
}

// readLimited reads at most limit bytes, returning errObjectTooLarge if the reader holds more.
func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	// Reading one byte past the limit distinguishes at-limit from over-limit, except when the
	// limit is math.MaxInt64: there limit+1 would overflow to a negative value, which LimitReader
	// treats as zero, silently reading every object as empty. No probe byte is possible or needed
	// at that limit, since a reader cannot hold more than math.MaxInt64 bytes.
	probe := limit + 1
	if probe < 0 {
		probe = math.MaxInt64
	}
	data, err := io.ReadAll(io.LimitReader(reader, probe))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errObjectTooLarge
	}
	return data, nil
}

// describeGetObjectError wraps a GetObject failure with an actionable message for the common
// failure classes, keeping the original error in the chain for errors.As/errors.Is.
func (s *s3DataInitializer) describeGetObjectError(objectKey string, err error) error {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchKey":
			return fmt.Errorf("object %q was not found in S3 bucket %q, so this data tier will be skipped: %w",
				objectKey, s.bucket, err)
		case "AccessDenied":
			return fmt.Errorf(
				"access to object %q in S3 bucket %q was denied; check the AWS credentials and the bucket policy: %w",
				objectKey, s.bucket, err)
		case "PermanentRedirect":
			return fmt.Errorf(
				"S3 bucket %q is served from a different region than the S3 client is configured for: %w",
				s.bucket, err)
		}
	}
	var respErr *awshttp.ResponseError
	if errors.As(err, &respErr) && respErr.HTTPStatusCode() >= 500 {
		return fmt.Errorf("S3 server errors getting object %q from bucket %q persisted after retries: %w",
			objectKey, s.bucket, err)
	}
	return fmt.Errorf("unable to get object %q from S3 bucket %q: %w", objectKey, s.bucket, err)
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
