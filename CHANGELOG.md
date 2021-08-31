# Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.5] - 2021-08-30
### Fixed
* Allow the alias relation `...` to be implicitly defined.

## [0.1.4] - 2021-08-18
### Fixed
* Bug with cross namespace config snapshots not being uniquely stored per namespace within the context.

## [0.1.3] - 2021-07-09
### Removed
* `google.protobuf.FieldMask` option in the `ListRelationTuples` RPC. We can re-introduce
  it later if needed.

## [0.1.2] - 2021-06-28
### Added
* gRPC Health Checking

## [0.1.1] - 2021-06-25
### Added
* RPC input request validation

## [0.1.0] - 2021-06-18
### Added
* Initial `WriteService`, `ReadService`, `CheckService`, and `ExpandService` implementations
* Namespace config API and continuous namespace config snapshot monitoring
* Gossip based clustering and consistent hashing with bounded load-balancing for `Check` RPCs
* gRPC or HTTP/JSON (or both) API interfaces
* Kubernetes Helm chart

[Unreleased]: https://github.com/authorizer-tech/access-controller/compare/v0.1.5...HEAD
[0.1.5]: https://github.com/authorizer-tech/access-controller/compare/v0.1.4...v0.1.5
[0.1.4]: https://github.com/authorizer-tech/access-controller/compare/v0.1.3...v0.1.4
[0.1.3]: https://github.com/authorizer-tech/access-controller/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/authorizer-tech/access-controller/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/authorizer-tech/access-controller/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/authorizer-tech/access-controller/releases/tag/v0.1.0