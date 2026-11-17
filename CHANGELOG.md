# Changelog

All notable changes to gox-packages are documented here.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

---

## [Unreleased]

### Added

- **blobs** — `S3Provider` now fully implemented with `aws-sdk-go-v2`; supports custom endpoints (MinIO, Wasabi) and static credentials; `BlobsConfig` gains `S3AccessKey` / `S3SecretKey`
- **core** — `image` package: `Service.New(Options)` generates SVG images in solid, gradient, and pixel (8×8 block) variants

### Fixed

- **blobs** — the `s3` driver's `CompleteMultipart` works on services that keep no part checksums (Ceph RGW before Squid, e.g. Linode Object Storage): `UploadPart` writes a marker per part under `.os-multipart/` (hidden from `List`, deleted on complete and abort) that the completion checks; `s3test` gains `NoPartChecksums`; `storetest` gains `Config.MinPartSize` for real S3 services, and its streaming test takes its memory baseline after the write

---

## Previous

See [../VERIFY.md](../VERIFY.md) for the full feature-parity audit with ntx-packages.
