# Changelog

## [0.2.0](https://github.com/launchdarkly/go-server-sdk-s3/compare/v0.1.1...v0.2.0) (2026-10-16)


### ⚠ BREAKING CHANGES

* Reduce the initializer to a single object key ([#14](https://github.com/launchdarkly/go-server-sdk-s3/issues/14))
* Remove flagValues support from the document format ([#13](https://github.com/launchdarkly/go-server-sdk-s3/issues/13))

### Features

* Reduce the initializer to a single object key ([#14](https://github.com/launchdarkly/go-server-sdk-s3/issues/14)) ([84d0673](https://github.com/launchdarkly/go-server-sdk-s3/commit/84d06730ff5098a7e3148f3416c6ae278aca9e74))
* Remove flagValues support from the document format ([#13](https://github.com/launchdarkly/go-server-sdk-s3/issues/13)) ([191c553](https://github.com/launchdarkly/go-server-sdk-s3/commit/191c55319e05f1dd70a68c907909c49cef955a25))
* Update to go-server-sdk 7.17.0 and adapt end-to-end tests ([#12](https://github.com/launchdarkly/go-server-sdk-s3/issues/12)) ([2a167ef](https://github.com/launchdarkly/go-server-sdk-s3/commit/2a167efaa2766b1c849b6e2efee5bcfa6c6ade1d))

## [0.1.1](https://github.com/launchdarkly/go-server-sdk-s3/compare/v0.1.0...v0.1.1) (2026-09-02)


### Bug Fixes

* prevent MaxObjectSize(math.MaxInt64) from reading every object as empty ([#8](https://github.com/launchdarkly/go-server-sdk-s3/issues/8)) ([867230e](https://github.com/launchdarkly/go-server-sdk-s3/commit/867230eff4eab498df479fc86b7c4ff05f374291))

## [0.1.0](https://github.com/launchdarkly/go-server-sdk-s3/compare/v0.0.1...v0.1.0) (2026-08-31)


### Features

* Add S3 data initializer for the FDv2 data system ([#2](https://github.com/launchdarkly/go-server-sdk-s3/issues/2)) ([595be5d](https://github.com/launchdarkly/go-server-sdk-s3/commit/595be5d427fc6e979bc772a98f754533e3c0580e))
* Bound S3 downloads and add fetch timeout, gzip support, and error classification ([#4](https://github.com/launchdarkly/go-server-sdk-s3/issues/4)) ([da7f5e9](https://github.com/launchdarkly/go-server-sdk-s3/commit/da7f5e9138f8e658372b7e823d823ef14be14694))
* Validate snapshot documents before applying them ([#3](https://github.com/launchdarkly/go-server-sdk-s3/issues/3)) ([800132d](https://github.com/launchdarkly/go-server-sdk-s3/commit/800132de7943ec93362dbec5b5c7e381da4a8284))
