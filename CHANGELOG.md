# Changelog

All notable changes to gox-packages are documented here.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

---

## [Unreleased]

### Added

- **blobs** — `S3Provider` now fully implemented with `aws-sdk-go-v2`; supports custom endpoints (MinIO, Wasabi) and static credentials; `BlobsConfig` gains `S3AccessKey` / `S3SecretKey`
- **core** — `image` package: `Service.New(Options)` generates SVG images in solid, gradient, and pixel (8×8 block) variants

---

## Previous

See [../VERIFY.md](../VERIFY.md) for the full feature-parity audit with ntx-packages.
