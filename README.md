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

## Data format

Objects must contain flag data in the same JSON or YAML document format used by the SDK's file data sources, with any combination of `flags`, `flagValues`, and `segments` properties:

```json
{
  "flagValues": {
    "my-boolean-flag-key": true,
    "my-string-flag-key": "value"
  }
}
```

See ["Reading flags from a file"](https://docs.launchdarkly.com/sdk/features/flags-from-files) for a full description of the format. When multiple object keys are given, their contents are merged; by default a flag or segment key appearing in more than one object is an error, which can be changed with `DuplicateKeysHandling`.

## Initialization semantics

Data loaded from S3 is a snapshot: it has no FDv2 payload state, so the SDK treats it as "cached" data rather than "refreshed" (current) data. Flag evaluations serve the snapshot values immediately, but if data sources are configured, `MakeCustomClient` will keep waiting for a synchronizer to reach fresh data; if none can, it returns an initialization error even though the snapshot was applied. Applications using this initializer as an outage fallback should treat that error as a degraded-but-alive condition rather than a fatal one -- check `client.Initialized()`, which is true once the snapshot is applied.

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
