// Package lds3 provides an Amazon S3 data initializer for the LaunchDarkly Go SDK's data system.
//
// The initializer reads one or more objects from an S3 bucket at startup and uses their contents
// as the SDK's initial set of flag and segment data. Initializers are one-shot: the SDK calls the
// initializer once during startup, and any ongoing updates must come from a separately configured
// synchronizer (or from nothing at all, if the S3 data is meant to be the only data source).
//
// A typical use is a two-tier startup strategy: try LaunchDarkly first, and fall back to a
// known-good snapshot stored in S3 so that the application can always start, even during a
// network or service outage:
//
//	import (
//	    ld "github.com/launchdarkly/go-server-sdk/v7"
//	    "github.com/launchdarkly/go-server-sdk/v7/ldcomponents"
//	    lds3 "github.com/launchdarkly/go-server-sdk-s3"
//	)
//
//	config := ld.Config{
//	    DataSystem: ldcomponents.DataSystem().Custom().
//	        Initializers(
//	            ldcomponents.PollingDataSourceV2().AsInitializer(),
//	            lds3.DataInitializer("my-bucket", "flags/snapshot.json"),
//	        ).
//	        Synchronizers(ldcomponents.StreamingDataSourceV2()),
//	}
//	client, err := ld.MakeCustomClient("sdk-key", config, 5*time.Second)
//
// The objects must be in the same JSON or YAML document format that the SDK's file data sources
// use, containing any combination of "flags", "flagValues", and "segments" properties. See the
// SDK's ldfiledata package documentation for a description of the format.
//
// By default, the initializer creates an S3 client from the default AWS configuration sources
// (environment variables, shared config files, and instance metadata). Use the builder's methods
// to supply a custom aws.Config or a preconfigured client.
//
// The initializer validates what it fetches: documents must be valid UTF-8, may be
// gzip-compressed when the object's Content-Encoding says so, are limited to a configurable
// maximum size, and a snapshot containing no flag or segment data is rejected rather than
// applied. The whole fetch is bounded by a configurable timeout so that a slow S3 request
// cannot consume the SDK's entire initialization wait budget.
package lds3
