package lds3

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
	"github.com/launchdarkly/go-server-sdk-evaluation/v3/ldbuilders"
	"github.com/launchdarkly/go-server-sdk-evaluation/v3/ldmodel"
	ld "github.com/launchdarkly/go-server-sdk/v7"
	"github.com/launchdarkly/go-server-sdk/v7/interfaces"
	"github.com/launchdarkly/go-server-sdk/v7/ldcomponents"
	"github.com/launchdarkly/go-server-sdk/v7/subsystems"
	"github.com/launchdarkly/go-server-sdk/v7/subsystems/ldstoreimpl"
	"github.com/launchdarkly/go-server-sdk/v7/subsystems/ldstoretypes"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests exercise the initializer through the full SDK client, verifying the initialization
// semantics documented in the README: snapshot data has no selector, and the SDK completes
// initialization when an initializer supplies data without one.

// fakeSynchronizer is a minimal DataSynchronizer that immediately delivers one change set.
type fakeSynchronizer struct {
	changeSet *subsystems.ChangeSet
}

func (f *fakeSynchronizer) Name() string { return "FakeSynchronizer" }

func (f *fakeSynchronizer) Fetch(
	subsystems.DataSelector, context.Context,
) (*subsystems.Basis, bool, error) {
	return nil, false, errors.New("not used as an initializer")
}

func (f *fakeSynchronizer) Sync(subsystems.DataSelector) <-chan subsystems.DataSynchronizerResult {
	results := make(chan subsystems.DataSynchronizerResult, 1)
	results <- subsystems.DataSynchronizerResult{
		ChangeSet: f.changeSet,
		State:     interfaces.DataSourceStateValid,
	}
	return results
}

func (f *fakeSynchronizer) Close() error { return nil }

type fakeSynchronizerConfigurer struct {
	synchronizer *fakeSynchronizer
}

func (c fakeSynchronizerConfigurer) Build(
	subsystems.ClientContext,
) (subsystems.DataSynchronizer, error) {
	return c.synchronizer, nil
}

// fullTransferChangeSet builds a change set as a synchronizer with current data would deliver it.
func fullTransferChangeSet(
	t *testing.T, selector subsystems.Selector, flags ...ldmodel.FeatureFlag,
) *subsystems.ChangeSet {
	t.Helper()
	intent := subsystems.ServerIntent{
		Payload: subsystems.Payload{
			ID:     "fake-payload",
			Target: 1,
			Code:   subsystems.IntentTransferFull,
			Reason: "payload-missing",
		},
	}
	items := make([]ldstoretypes.KeyedItemDescriptor, 0, len(flags))
	for i := range flags {
		flag := flags[i]
		items = append(items, ldstoretypes.KeyedItemDescriptor{
			Key:  flag.Key,
			Item: ldstoretypes.ItemDescriptor{Version: flag.Version, Item: &flag},
		})
	}
	collections := []ldstoretypes.Collection{{Kind: ldstoreimpl.Features(), Items: items}}
	changeSet, err := subsystems.NewChangeSetFromCollections(intent, selector, collections)
	require.NoError(t, err)
	return changeSet
}

// A snapshot has no selector, but applied initializer data completes initialization:
// MakeCustomClient succeeds, Initialized() is true, and evaluations serve the S3 data.
func TestClientEvaluatesFlagsFromS3(t *testing.T) {
	fake := &fakeS3Client{objects: simpleObjects(map[string][]byte{
		"flags.json": []byte(`{"flags": {"bool-flag": {"key": "bool-flag", "version": 1, "on": true, "fallthrough": {"variation": 0}, "variations": [true, false]}}}`),
	})}
	config := ld.Config{
		Events: ldcomponents.NoEvents(),
		DataSystem: ldcomponents.DataSystem().Custom().Initializers(
			DataInitializer("my-bucket", "flags.json").S3Client(fake),
		),
	}

	client, err := ld.MakeCustomClient("sdk-key", config, time.Second*5)
	require.NotNil(t, client)
	defer client.Close() //nolint:errcheck // Close errors are irrelevant to these assertions.
	assert.NoError(t, err, "selector-less initializer data completes initialization")

	assert.True(t, client.Initialized())
	value, err := client.BoolVariation("bool-flag", ldcontext.New("user-key"), false)
	assert.NoError(t, err)
	assert.True(t, value)
}

// With a synchronizer configured, the S3 snapshot serves only until the synchronizer delivers
// current data; the synchronizer's data then replaces the snapshot.
func TestClientDefersToSynchronizerForFreshData(t *testing.T) {
	fake := &fakeS3Client{objects: simpleObjects(map[string][]byte{
		"flags.json": []byte(`{"flags": {"bool-flag": {"key": "bool-flag", "version": 1, "on": true, "fallthrough": {"variation": 0}, "variations": [false, true]}}}`),
	})}
	synchronizerFlag := ldbuilders.NewFlagBuilder("bool-flag").Version(1).
		SingleVariation(ldvalue.Bool(true)).Build()
	synchronizer := &fakeSynchronizer{
		changeSet: fullTransferChangeSet(t, subsystems.NewSelector("payload-state", 1), synchronizerFlag),
	}
	config := ld.Config{
		Events: ldcomponents.NoEvents(),
		DataSystem: ldcomponents.DataSystem().Custom().
			Initializers(DataInitializer("my-bucket", "flags.json").S3Client(fake)).
			Synchronizers(fakeSynchronizerConfigurer{synchronizer}),
	}

	client, err := ld.MakeCustomClient("sdk-key", config, time.Second*5)
	require.NoError(t, err)
	defer client.Close() //nolint:errcheck // Close errors are irrelevant to these assertions.

	reached := client.GetDataSourceStatusProvider().WaitFor(interfaces.DataSourceStateValid, time.Second*5)
	require.True(t, reached, "timed out waiting for the data source to become valid")

	// Initialization completes on the snapshot alone, so the synchronizer's data can arrive
	// after MakeCustomClient returns; wait for it to replace the snapshot value.
	require.Eventually(t, func() bool {
		value, err := client.BoolVariation("bool-flag", ldcontext.New("user-key"), false)
		return err == nil && value
	}, time.Second*5, time.Millisecond*10, "the synchronizer's data should replace the S3 snapshot")

	assert.Equal(t, []getObjectRequest{{bucket: "my-bucket", key: "flags.json"}}, fake.requests,
		"the S3 initializer should still have run first")
}

// When an S3 initializer fails, the SDK falls through to the next configured initializer.
func TestClientFallsThroughToNextInitializerOnFailure(t *testing.T) {
	failing := &fakeS3Client{objects: map[string]fakeObject{}}
	good := &fakeS3Client{objects: simpleObjects(map[string][]byte{
		"flags.json": []byte(`{"flags": {"bool-flag": {"key": "bool-flag", "version": 1, "on": true, "fallthrough": {"variation": 0}, "variations": [true, false]}}}`),
	})}
	config := ld.Config{
		Events: ldcomponents.NoEvents(),
		DataSystem: ldcomponents.DataSystem().Custom().Initializers(
			DataInitializer("bucket-a", "missing.json").S3Client(failing),
			DataInitializer("bucket-b", "flags.json").S3Client(good),
		),
	}

	client, err := ld.MakeCustomClient("sdk-key", config, time.Second*5)
	require.NotNil(t, client)
	defer client.Close() //nolint:errcheck // Close errors are irrelevant to these assertions.
	assert.NoError(t, err, "selector-less initializer data completes initialization")

	assert.True(t, client.Initialized(), "the second initializer's data should have been applied")
	value, err := client.BoolVariation("bool-flag", ldcontext.New("user-key"), false)
	assert.NoError(t, err)
	assert.True(t, value)

	assert.NotEmpty(t, failing.requests, "the failing initializer should have been tried first")
}
