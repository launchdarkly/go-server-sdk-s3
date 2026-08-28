package lds3

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
	"github.com/launchdarkly/go-server-sdk-evaluation/v3/ldmodel"
	"github.com/launchdarkly/go-server-sdk/v7/subsystems"
	"github.com/launchdarkly/go-server-sdk/v7/subsystems/ldstoretypes"

	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type getObjectRequest struct {
	bucket string
	key    string
}

// fakeObject is an object served by fakeS3Client, with optional response attributes.
type fakeObject struct {
	data            []byte
	contentEncoding string
	contentLength   *int64 // overrides the real length when set, to simulate a lying header
}

// fakeS3Client is a test implementation of S3Client that serves objects from a map.
type fakeS3Client struct {
	objects  map[string]fakeObject
	getErr   error // when set, returned for every GetObject call
	requests []getObjectRequest
}

// simpleObjects makes fakeS3Client objects with no special response attributes.
func simpleObjects(data map[string][]byte) map[string]fakeObject {
	objects := make(map[string]fakeObject, len(data))
	for key, value := range data {
		objects[key] = fakeObject{data: value}
	}
	return objects
}

func (f *fakeS3Client) GetObject(
	_ context.Context, params *s3.GetObjectInput, _ ...func(*s3.Options),
) (*s3.GetObjectOutput, error) {
	f.requests = append(f.requests, getObjectRequest{bucket: *params.Bucket, key: *params.Key})
	if f.getErr != nil {
		return nil, f.getErr
	}
	obj, ok := f.objects[*params.Key]
	if !ok {
		return nil, &types.NoSuchKey{}
	}
	contentLength := aws.Int64(int64(len(obj.data)))
	if obj.contentLength != nil {
		contentLength = obj.contentLength
	}
	output := &s3.GetObjectOutput{
		Body:          io.NopCloser(bytes.NewReader(obj.data)),
		ContentLength: contentLength,
	}
	if obj.contentEncoding != "" {
		output.ContentEncoding = aws.String(obj.contentEncoding)
	}
	return output, nil
}

func buildInitializer(t *testing.T, b *DataInitializerBuilder) subsystems.DataInitializer {
	t.Helper()
	initializer, err := b.Build(subsystems.BasicClientContext{})
	require.NoError(t, err)
	return initializer
}

func fetchBasis(t *testing.T, initializer subsystems.DataInitializer) *subsystems.Basis {
	t.Helper()
	basis, fallback, err := initializer.Fetch(nil, context.Background())
	require.NoError(t, err)
	require.False(t, fallback)
	require.NotNil(t, basis)
	return basis
}

func fetchError(t *testing.T, b *DataInitializerBuilder) error {
	t.Helper()
	initializer := buildInitializer(t, b)
	basis, fallback, err := initializer.Fetch(nil, context.Background())
	assert.Nil(t, basis)
	assert.False(t, fallback)
	require.Error(t, err)
	return err
}

func collectionByKind(
	t *testing.T, basis *subsystems.Basis, kindName string,
) []ldstoretypes.KeyedItemDescriptor {
	t.Helper()
	collections, err := basis.ChangeSet.Collections()
	require.NoError(t, err)
	for _, c := range collections {
		if c.Kind.GetName() == kindName {
			return c.Items
		}
	}
	return nil
}

func itemKeys(items []ldstoretypes.KeyedItemDescriptor) []string {
	keys := make([]string, 0, len(items))
	for _, item := range items {
		keys = append(keys, item.Key)
	}
	return keys
}

func gzipCompress(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, err := gz.Write(data)
	require.NoError(t, err)
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

func TestBuildRequiresBucketAndObjectKeys(t *testing.T) {
	_, err := DataInitializer("", "key").Build(subsystems.BasicClientContext{})
	assert.Error(t, err)

	_, err = DataInitializer("bucket").Build(subsystems.BasicClientContext{})
	assert.Error(t, err)

	_, err = DataInitializer("bucket").ObjectKeys("key").Build(subsystems.BasicClientContext{})
	assert.NoError(t, err)
}

func TestBuildValidatesMaxObjectSizeAndTimeout(t *testing.T) {
	_, err := DataInitializer("bucket", "key").MaxObjectSize(0).Build(subsystems.BasicClientContext{})
	assert.Error(t, err)

	_, err = DataInitializer("bucket", "key").MaxObjectSize(-1).Build(subsystems.BasicClientContext{})
	assert.Error(t, err)

	_, err = DataInitializer("bucket", "key").Timeout(-time.Second).Build(subsystems.BasicClientContext{})
	assert.Error(t, err)

	_, err = DataInitializer("bucket", "key").MaxObjectSize(1024).Timeout(0).
		Build(subsystems.BasicClientContext{})
	assert.NoError(t, err)
}

func TestInitializerName(t *testing.T) {
	initializer := buildInitializer(t, DataInitializer("bucket", "key").S3Client(&fakeS3Client{}))
	assert.Equal(t, "S3DataInitializer", initializer.Name())
}

func TestFetchLoadsFlagsAndSegmentsFromJSON(t *testing.T) {
	fake := &fakeS3Client{objects: simpleObjects(map[string][]byte{
		"flags.json": []byte(`{
			"flags": {
				"flag1": {"key": "flag1", "version": 2, "on": true}
			},
			"flagValues": {
				"flag2": "value2"
			},
			"segments": {
				"seg1": {"key": "seg1", "version": 3}
			}
		}`),
	})}
	initializer := buildInitializer(t, DataInitializer("my-bucket", "flags.json").S3Client(fake))
	basis := fetchBasis(t, initializer)

	assert.False(t, basis.Persist)
	assert.Equal(t, subsystems.IntentTransferFull, basis.ChangeSet.IntentCode())
	assert.False(t, basis.ChangeSet.Selector().IsDefined())

	flags := collectionByKind(t, basis, "features")
	assert.ElementsMatch(t, []string{"flag1", "flag2"}, itemKeys(flags))
	for _, item := range flags {
		flag := item.Item.Item.(*ldmodel.FeatureFlag)
		assert.Equal(t, item.Key, flag.Key)
		assert.Equal(t, item.Item.Version, flag.Version)
		if item.Key == "flag1" {
			assert.Equal(t, 2, flag.Version)
		} else {
			// flag2 came from a flagValues entry, expanded into a flag returning that value
			assert.Equal(t, []ldvalue.Value{ldvalue.String("value2")}, flag.Variations)
		}
	}

	segments := collectionByKind(t, basis, "segments")
	require.Len(t, segments, 1)
	assert.Equal(t, "seg1", segments[0].Key)
	assert.Equal(t, 3, segments[0].Item.Version)

	assert.Equal(t, []getObjectRequest{{bucket: "my-bucket", key: "flags.json"}}, fake.requests)
}

func TestFetchLoadsYAML(t *testing.T) {
	fake := &fakeS3Client{objects: simpleObjects(map[string][]byte{
		"flags.yaml": []byte("flagValues:\n  yaml-flag: 3\n"),
	})}
	initializer := buildInitializer(t, DataInitializer("my-bucket", "flags.yaml").S3Client(fake))
	basis := fetchBasis(t, initializer)

	flags := collectionByKind(t, basis, "features")
	require.Len(t, flags, 1)
	assert.Equal(t, "yaml-flag", flags[0].Key)
}

func TestFetchAllowsUnknownTopLevelProperties(t *testing.T) {
	fake := &fakeS3Client{objects: simpleObjects(map[string][]byte{
		"flags.json": []byte(`{"flagValues": {"flag-a": 1}, "metadata": {"generatedAt": "sometime"}}`),
	})}
	initializer := buildInitializer(t, DataInitializer("my-bucket", "flags.json").S3Client(fake))
	basis := fetchBasis(t, initializer)

	flags := collectionByKind(t, basis, "features")
	assert.Equal(t, []string{"flag-a"}, itemKeys(flags))
}

func TestFetchMergesMultipleObjects(t *testing.T) {
	fake := &fakeS3Client{objects: simpleObjects(map[string][]byte{
		"a.json": []byte(`{"flagValues": {"flag-a": 1}}`),
		"b.json": []byte(`{"flagValues": {"flag-b": 2}}`),
	})}
	initializer := buildInitializer(t, DataInitializer("my-bucket", "a.json", "b.json").S3Client(fake))
	basis := fetchBasis(t, initializer)

	flags := collectionByKind(t, basis, "features")
	assert.Equal(t, []string{"flag-a", "flag-b"}, itemKeys(flags))
	assert.Equal(t, []getObjectRequest{
		{bucket: "my-bucket", key: "a.json"},
		{bucket: "my-bucket", key: "b.json"},
	}, fake.requests)
}

func TestFetchFailsOnDuplicateKeysAcrossObjectsByDefault(t *testing.T) {
	fake := &fakeS3Client{objects: simpleObjects(map[string][]byte{
		"a.json": []byte(`{"flagValues": {"flag-a": 1}}`),
		"b.json": []byte(`{"flagValues": {"flag-a": 2}}`),
	})}
	err := fetchError(t, DataInitializer("my-bucket", "a.json", "b.json").S3Client(fake))
	assert.Contains(t, err.Error(), "flag-a")
	assert.Contains(t, err.Error(), "multiple objects")
}

func TestFetchFailsOnDuplicateKeyWithinOneObject(t *testing.T) {
	fake := &fakeS3Client{objects: simpleObjects(map[string][]byte{
		"a.json": []byte(`{"flags": {"flag-a": {"key": "flag-a"}}, "flagValues": {"flag-a": 1}}`),
	})}
	err := fetchError(t, DataInitializer("my-bucket", "a.json").S3Client(fake))
	assert.Contains(t, err.Error(), "flag-a")
	assert.Contains(t, err.Error(), "same object")
	assert.NotContains(t, err.Error(), "multiple objects")
}

func TestFetchCanIgnoreDuplicateKeys(t *testing.T) {
	fake := &fakeS3Client{objects: simpleObjects(map[string][]byte{
		"a.json": []byte(`{"flagValues": {"flag-a": 1}}`),
		"b.json": []byte(`{"flagValues": {"flag-a": 2}}`),
	})}
	initializer := buildInitializer(t, DataInitializer("my-bucket", "a.json", "b.json").
		DuplicateKeysHandling(DuplicateKeysKeepFirst).
		S3Client(fake))
	basis := fetchBasis(t, initializer)

	flags := collectionByKind(t, basis, "features")
	require.Len(t, flags, 1)
	flag := flags[0].Item.Item.(*ldmodel.FeatureFlag)
	assert.Equal(t, []ldvalue.Value{ldvalue.Int(1)}, flag.Variations,
		"the first occurrence of a duplicated key should win")
}

func TestFetchRejectsDocumentsWithNoItems(t *testing.T) {
	for name, data := range map[string][]byte{
		"empty body":            {},
		"whitespace only":       []byte("   \n   \n"),
		"JSON null":             []byte(`null`),
		"empty JSON object":     []byte(`{}`),
		"empty properties":      []byte(`{"flags": {}, "flagValues": {}, "segments": {}}`),
		"misspelled properties": []byte(`{"flagz": {"flag-a": {"key": "flag-a"}}}`),
		"YAML null document":    []byte("null\n"),
	} {
		t.Run(name, func(t *testing.T) {
			fake := &fakeS3Client{objects: map[string]fakeObject{"flags.json": {data: data}}}
			err := fetchError(t, DataInitializer("my-bucket", "flags.json").S3Client(fake))
			assert.Contains(t, err.Error(), "no flag or segment data")
		})
	}
}

func TestFetchRejectsDocumentsThatDoNotParse(t *testing.T) {
	for name, data := range map[string][]byte{
		"malformed JSON":  []byte(`{"flags": what}`),
		"JSON array":      []byte(`[1, 2, 3]`),
		"HTML error page": []byte(`<html><body>503 Service Unavailable</body></html>`),
		"YAML wrong type": []byte("flags: 3\n"),
		"tab whitespace":  []byte("   \n\t  "),
		"null flag entry": []byte(`{"flags": {"flag-a": null}}`),
		"leading NBSP":    append([]byte{0xC2, 0xA0}, []byte(`{"flagValues": {"flag-a": 1}}`)...),
	} {
		t.Run(name, func(t *testing.T) {
			fake := &fakeS3Client{objects: map[string]fakeObject{"flags.json": {data: data}}}
			err := fetchError(t, DataInitializer("my-bucket", "flags.json").S3Client(fake))
			assert.Contains(t, err.Error(), "flags.json")
		})
	}
}

func TestFetchRejectsMismatchedItemKeys(t *testing.T) {
	fake := &fakeS3Client{objects: simpleObjects(map[string][]byte{
		"flags.json": []byte(`{"flags": {"flag-a": {"key": "flag-b"}}}`),
	})}
	err := fetchError(t, DataInitializer("my-bucket", "flags.json").S3Client(fake))
	assert.Contains(t, err.Error(), "flag-a")
	assert.Contains(t, err.Error(), "mismatched")

	fake = &fakeS3Client{objects: simpleObjects(map[string][]byte{
		"flags.json": []byte(`{"segments": {"seg-a": {"key": "seg-b"}}}`),
	})}
	err = fetchError(t, DataInitializer("my-bucket", "flags.json").S3Client(fake))
	assert.Contains(t, err.Error(), "seg-a")
	assert.Contains(t, err.Error(), "mismatched")
}

func TestFetchRejectsNegativeVersions(t *testing.T) {
	fake := &fakeS3Client{objects: simpleObjects(map[string][]byte{
		"flags.json": []byte(`{"flags": {"flag-a": {"key": "flag-a", "version": -1}}}`),
	})}
	err := fetchError(t, DataInitializer("my-bucket", "flags.json").S3Client(fake))
	assert.Contains(t, err.Error(), "negative version")

	fake = &fakeS3Client{objects: simpleObjects(map[string][]byte{
		"flags.json": []byte(`{"segments": {"seg-a": {"key": "seg-a", "version": -2}}}`),
	})}
	err = fetchError(t, DataInitializer("my-bucket", "flags.json").S3Client(fake))
	assert.Contains(t, err.Error(), "negative version")
}

func TestFetchRejectsInvalidUTF8(t *testing.T) {
	fake := &fakeS3Client{objects: simpleObjects(map[string][]byte{
		"flags.json": append([]byte(`{"flagValues": {"fl`), 0xFF, 0xFE, '"', ':', '1', '}', '}'),
	})}
	err := fetchError(t, DataInitializer("my-bucket", "flags.json").S3Client(fake))
	assert.Contains(t, err.Error(), "not valid UTF-8")
}

func TestFetchDecompressesGzipContentEncoding(t *testing.T) {
	fake := &fakeS3Client{objects: map[string]fakeObject{
		"flags.json.gz": {
			data:            gzipCompress(t, []byte(`{"flagValues": {"gz-flag": true}}`)),
			contentEncoding: "gzip",
		},
	}}
	initializer := buildInitializer(t, DataInitializer("my-bucket", "flags.json.gz").S3Client(fake))
	basis := fetchBasis(t, initializer)

	flags := collectionByKind(t, basis, "features")
	assert.Equal(t, []string{"gz-flag"}, itemKeys(flags))
}

func TestFetchRejectsGzipDataWithoutContentEncoding(t *testing.T) {
	fake := &fakeS3Client{objects: map[string]fakeObject{
		"flags.json.gz": {data: gzipCompress(t, []byte(`{"flagValues": {"gz-flag": true}}`))},
	}}
	err := fetchError(t, DataInitializer("my-bucket", "flags.json.gz").S3Client(fake))
	assert.Contains(t, err.Error(), "gzip-compressed")
	assert.Contains(t, err.Error(), "Content-Encoding")
}

func TestFetchRejectsObjectOverSizeLimitByContentLength(t *testing.T) {
	fake := &fakeS3Client{objects: map[string]fakeObject{
		"flags.json": {
			data:          []byte(`{"flagValues": {"flag-a": 1}}`),
			contentLength: aws.Int64(200),
		},
	}}
	err := fetchError(t, DataInitializer("my-bucket", "flags.json").MaxObjectSize(100).S3Client(fake))
	assert.Contains(t, err.Error(), "exceeds")
	assert.Contains(t, err.Error(), "MaxObjectSize")
}

func TestFetchRejectsObjectOverSizeLimitWithUnderstatedContentLength(t *testing.T) {
	bigData := append([]byte(`{"flagValues": {"flag-a": "`),
		append(bytes.Repeat([]byte{'x'}, 200), []byte(`"}}`)...)...)
	fake := &fakeS3Client{objects: map[string]fakeObject{
		"flags.json": {data: bigData, contentLength: aws.Int64(10)},
	}}
	err := fetchError(t, DataInitializer("my-bucket", "flags.json").MaxObjectSize(100).S3Client(fake))
	assert.Contains(t, err.Error(), "exceeds")
}

func TestFetchRejectsGzipObjectWhoseDecompressedContentExceedsLimit(t *testing.T) {
	document := append([]byte(`{"flagValues": {"flag-a": "`),
		append(bytes.Repeat([]byte{'x'}, 2048), []byte(`"}}`)...)...)
	fake := &fakeS3Client{objects: map[string]fakeObject{
		"flags.json.gz": {data: gzipCompress(t, document), contentEncoding: "gzip"},
	}}
	err := fetchError(t,
		DataInitializer("my-bucket", "flags.json.gz").MaxObjectSize(1024).S3Client(fake))
	assert.Contains(t, err.Error(), "exceeds")
}

func TestFetchAcceptsObjectWithinRaisedSizeLimit(t *testing.T) {
	data := []byte(`{"flagValues": {"flag-a": 1}}`)
	fake := &fakeS3Client{objects: simpleObjects(map[string][]byte{"flags.json": data})}
	initializer := buildInitializer(t,
		DataInitializer("my-bucket", "flags.json").MaxObjectSize(int64(len(data))).S3Client(fake))
	basis := fetchBasis(t, initializer)
	assert.Equal(t, []string{"flag-a"}, itemKeys(collectionByKind(t, basis, "features")))
}

func TestFetchReturnsErrorForMissingObject(t *testing.T) {
	fake := &fakeS3Client{objects: map[string]fakeObject{}}
	err := fetchError(t, DataInitializer("my-bucket", "missing.json").S3Client(fake))
	assert.Contains(t, err.Error(), "missing.json")
	assert.Contains(t, err.Error(), "my-bucket")
	assert.Contains(t, err.Error(), "skipped")
	var noSuchKey *types.NoSuchKey
	assert.ErrorAs(t, err, &noSuchKey)
}

func TestFetchDescribesCommonS3ErrorClasses(t *testing.T) {
	accessDenied := &smithy.GenericAPIError{Code: "AccessDenied", Message: "Access Denied"}
	err := fetchError(t, DataInitializer("my-bucket", "key").S3Client(&fakeS3Client{getErr: accessDenied}))
	assert.Contains(t, err.Error(), "credentials")
	var apiErr smithy.APIError
	assert.ErrorAs(t, err, &apiErr)

	redirect := &smithy.GenericAPIError{Code: "PermanentRedirect", Message: "moved"}
	err = fetchError(t, DataInitializer("my-bucket", "key").S3Client(&fakeS3Client{getErr: redirect}))
	assert.Contains(t, err.Error(), "region")

	serverError := &awshttp.ResponseError{
		ResponseError: &smithyhttp.ResponseError{
			Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 503}},
			Err:      errors.New("service unavailable"),
		},
	}
	err = fetchError(t, DataInitializer("my-bucket", "key").S3Client(&fakeS3Client{getErr: serverError}))
	assert.Contains(t, err.Error(), "after retries")
}

type failingReader struct{}

func (r failingReader) Read([]byte) (int, error) { return 0, fmt.Errorf("read failure") }

type bodyErrorClient struct{}

func (c bodyErrorClient) GetObject(
	context.Context, *s3.GetObjectInput, ...func(*s3.Options),
) (*s3.GetObjectOutput, error) {
	return &s3.GetObjectOutput{Body: io.NopCloser(failingReader{})}, nil
}

func TestFetchReturnsErrorWhenBodyReadFails(t *testing.T) {
	initializer := buildInitializer(t, DataInitializer("my-bucket", "key").S3Client(bodyErrorClient{}))

	basis, _, err := initializer.Fetch(nil, context.Background())
	assert.Nil(t, basis)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read failure")
}

// blockingClient blocks every GetObject call until the request context is done.
type blockingClient struct{}

func (c blockingClient) GetObject(
	ctx context.Context, _ *s3.GetObjectInput, _ ...func(*s3.Options),
) (*s3.GetObjectOutput, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestFetchTimeoutBoundsTheWholeOperation(t *testing.T) {
	initializer := buildInitializer(t, DataInitializer("my-bucket", "key").
		Timeout(20*time.Millisecond).
		S3Client(blockingClient{}))

	basis, _, err := initializer.Fetch(nil, context.Background())
	assert.Nil(t, basis)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestFetchPreservesCallerCancellation(t *testing.T) {
	initializer := buildInitializer(t, DataInitializer("my-bucket", "key").S3Client(blockingClient{}))

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	basis, _, err := initializer.Fetch(nil, ctx)
	assert.Nil(t, basis)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// deadlineProbeClient records whether the request context had a deadline.
type deadlineProbeClient struct {
	hadDeadline bool
}

func (c *deadlineProbeClient) GetObject(
	ctx context.Context, _ *s3.GetObjectInput, _ ...func(*s3.Options),
) (*s3.GetObjectOutput, error) {
	_, c.hadDeadline = ctx.Deadline()
	data := []byte(`{"flagValues": {"flag-a": 1}}`)
	return &s3.GetObjectOutput{
		Body:          io.NopCloser(bytes.NewReader(data)),
		ContentLength: aws.Int64(int64(len(data))),
	}, nil
}

func TestFetchAppliesDefaultTimeoutAndZeroDisablesIt(t *testing.T) {
	probe := &deadlineProbeClient{}
	initializer := buildInitializer(t, DataInitializer("my-bucket", "key").S3Client(probe))
	fetchBasis(t, initializer)
	assert.True(t, probe.hadDeadline, "the default timeout should set a deadline")

	probe = &deadlineProbeClient{}
	initializer = buildInitializer(t, DataInitializer("my-bucket", "key").Timeout(0).S3Client(probe))
	fetchBasis(t, initializer)
	assert.False(t, probe.hadDeadline, "a zero timeout should leave the context unbounded")
}
