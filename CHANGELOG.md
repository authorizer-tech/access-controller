# Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]
### Added
* Initial `WriteService`, `ReadService`, `CheckService`, and `ExpandService` implementations
* Namespace config API and continuous namespace config snapshot monitoring
* Gossip based clustering and consistent hashing with bounded load-balancing for `Check` RPCs
* gRPC or HTTP/JSON (or both) API interfaces
* Kubernetes Helm chart

[Unreleased]: https://github.com/authorizer-tech/access-controller/tree/master