# Contributing to this library

The source code for this library is [here](https://github.com/launchdarkly/go-server-sdk-s3). This repository is currently internal to LaunchDarkly and is maintained by the SDK team; changes follow the same pull request and review process as our other SDK repositories. Since this library is meant to be used in conjunction with the LaunchDarkly Go SDK, you may want to look at the [Go SDK source code](https://github.com/launchdarkly/go-server-sdk) and our [SDK contributor's guide](https://docs.launchdarkly.com/sdk/concepts/contributors-guide).

## Submitting pull requests

Before submitting pull requests, ensure that all temporary or unintended code is removed. Don't worry about adding reviewers to the pull request; the LaunchDarkly SDK team will add themselves.

## Build instructions

### Prerequisites

This project should be built against the lowest supported Go version as described in [README.md](./README.md).

### Building

To build the library without running any tests:
```
make build
```

### Testing

To build the library and run all unit tests:
```
make test
```

The tests are self-contained: integration-style tests run against local `httptest` servers, so no AWS credentials or S3 access is needed.

### Linting

To run the linter:
```
make lint
```
