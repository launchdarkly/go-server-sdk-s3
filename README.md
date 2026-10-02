# LaunchDarkly Server-side SDK for Go - S3 data initializer

> This repository is currently internal to LaunchDarkly. The library is not yet published or
> supported for external use.

This library provides an Amazon S3 data initializer for the [LaunchDarkly Go SDK](https://github.com/launchdarkly/go-server-sdk)'s data system. It reads one or more objects from an S3 bucket at SDK startup and uses their contents as the SDK's initial flag and segment data.

Initializers are one-shot: the SDK runs them once during startup to get flag values available as quickly as possible. Ongoing updates, if any, come from separately configured synchronizers. A typical use for this initializer is as a fallback tier so that an application can always start with a known-good flag snapshot, even when LaunchDarkly is unreachable.

## Requirements

This library requires version 7.15.6 or higher of the LaunchDarkly Go SDK (the first release that includes the FDv2 data system's `subsystems.DataInitializer` interface), and Go 1.25 or higher.

## Usage

Import this package (with an alias for readability):

```go
import (
    lds3 "github.com/launchdarkly/go-server-sdk-s3"
)
```

Configure it as an initializer in the SDK's data system. For example, to try LaunchDarkly first and fall back to an S3 snapshot, with streaming for ongoing updates:

```go
config := ld.Config{
    DataSystem: ldcomponents.DataSystem().Custom().
        Initializers(
            ldcomponents.PollingDataSourceV2().AsInitializer(),
            lds3.DataInitializer("my-bucket", "flags/snapshot.json"),
        ).
        Synchronizers(ldcomponents.StreamingDataSourceV2()),
}
client, err := ld.MakeCustomClient("sdk-key", config, 5*time.Second)
```

By default, the S3 client is created from the default AWS configuration sources (environment variables, shared config files, instance metadata). To customize it:

```go
// With a specific AWS configuration:
lds3.DataInitializer("my-bucket", "snapshot.json").AWSConfig(myAWSConfig)

// Or with a preconfigured client, e.g. for an S3-compatible service:
lds3.DataInitializer("my-bucket", "snapshot.json").S3Client(myS3Client)
```

`S3Client` is the sanctioned way to use custom endpoints, path-style addressing, or local S3 stand-ins such as LocalStack or MinIO; the builder deliberately does not mirror the AWS SDK's endpoint options.

Two safety limits apply and can be adjusted:

```go
// The whole fetch (all objects) must finish within this time; default 30s. The limit keeps a
// slow S3 request from consuming the SDK's entire initialization wait budget. Zero disables it.
lds3.DataInitializer("my-bucket", "snapshot.json").Timeout(10 * time.Second)

// Each object's content (after decompression, if compressed) may be at most this many bytes;
// default 100MB.
lds3.DataInitializer("my-bucket", "snapshot.json").MaxObjectSize(500 * 1024 * 1024)
```

Objects may be gzip-compressed if their `Content-Encoding` metadata is set to `gzip`. An object that contains gzip data without that metadata is rejected with an explanatory error.

## Data format

Objects must contain flag data in the same JSON or YAML document format used by the SDK's file data sources, with any combination of `flags` and `segments` properties:

```json
{
  "flags": {
    "my-boolean-flag-key": {
      "key": "my-boolean-flag-key",
      "version": 1,
      "on": true,
      "fallthrough": { "variation": 0 },
      "variations": [true, false]
    }
  }
}
```

See ["Reading flags from a file"](https://docs.launchdarkly.com/sdk/features/flags-from-files) for a full description of the format. The file data sources' simplified `flagValues` entries are not supported: this initializer consumes snapshots of real flag data, which express full definitions, and an object that contains a `flagValues` section is rejected with an explanatory error. When multiple object keys are given, their contents are merged; by default a flag or segment key appearing in more than one object is an error, which can be changed with `DuplicateKeysHandling`.

Because this initializer's purpose is to provide a known-good fallback, it validates the data rather than applying whatever it finds:

* A snapshot that contains no flags and no segments is rejected. A corrupted, truncated, or misproduced object therefore causes a loud fallthrough to the next data source instead of silently turning off every flag. This also means a misspelled top-level property (for example `"flagz"`) is caught.
* Documents must be valid UTF-8.
* A flag or segment whose embedded `key` property does not match the key it is defined under is rejected, as is a negative `version`.

Prefer a single object per snapshot. Reads of multiple objects are sequential, so a publisher updating several objects concurrently can produce a mixed snapshot; if you use multiple objects, update them together and rely on the duplicate-keys check to catch overlap.

## Initialization semantics

Data loaded from S3 is a snapshot: it has no FDv2 payload state, so the SDK treats it as "cached" data rather than "refreshed" (current) data. As of Go SDK 7.17, applied snapshot data completes initialization: `MakeCustomClient` succeeds, `client.Initialized()` is true, and flag evaluations serve the snapshot values, while any configured synchronizers keep trying for current data in the background. (On earlier SDK versions, `MakeCustomClient` kept waiting for a synchronizer to reach fresh data and returned an initialization error if none could, even though the snapshot was applied and evaluations served it.)

Because of these semantics, place this initializer last in the initializer chain, after any initializers that can deliver current data from LaunchDarkly.

## Out of scope

This library initializes once at startup. It does not watch S3 for changes or act as a synchronizer, it does not ask the SDK to persist the snapshot into a persistent store (`Persist` is always false), and it does not support the older FDv1 data system.

## Contributing

This repository is maintained by the LaunchDarkly SDK team. While the repository is internal, changes follow the same pull request and review process as our other SDK repositories.

## About LaunchDarkly

* LaunchDarkly is a continuous delivery platform that provides feature flags as a service and allows developers to iterate quickly and safely. We allow you to easily flag your features and manage them from the LaunchDarkly dashboard.  With LaunchDarkly, you can:
    * Roll out a new feature to a subset of your users (like a group of users who opt-in to a beta tester group), gathering feedback and bug reports from real-world use cases.
    * Gradually roll out a feature to an increasing percentage of users, and track the effect that the feature has on key metrics (for instance, how likely is a user to complete a purchase if they have feature A versus feature B?).
    * Turn off a feature that you realize is causing performance problems in production, without needing to re-deploy, or even restart the application with a changed configuration file.
    * Grant access to certain features based on user attributes, like payment plan (eg: users on the 'gold' plan get access to more features than users in the 'silver' plan). Disable parts of your application to facilitate maintenance, without taking everything offline.
* LaunchDarkly provides feature flag SDKs for a wide variety of languages and technologies. Check out [our documentation](https://docs.launchdarkly.com/sdk) for a complete list.
* Explore LaunchDarkly
    * [launchdarkly.com](https://www.launchdarkly.com/ "LaunchDarkly Main Website") for more information
    * [docs.launchdarkly.com](https://docs.launchdarkly.com/  "LaunchDarkly Documentation") for our documentation and SDK reference guides
    * [apidocs.launchdarkly.com](https://apidocs.launchdarkly.com/  "LaunchDarkly API Documentation") for our API documentation
    * [blog.launchdarkly.com](https://blog.launchdarkly.com/  "LaunchDarkly Blog Documentation") for the latest product updates
