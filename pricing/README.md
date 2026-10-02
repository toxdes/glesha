# Storage estimates

`rates.go` is a private, read-only table of public USD storage rates verified
2026-10-01. Application code accesses it through the helpers in `pricing.go`;
it does not fetch prices at runtime. New releases can update the table.

Sources:

- [AWS S3 price list](https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonS3/current/index.json), published 2026-09-28.
- [AWS Glacier price list](https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonGlacier/current/index.json), published 2026-09-11.
- [AWS Deep Archive price list](https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonS3GlacierDeepArchive/current/index.json), published 2026-09-11.
- [Backblaze B2 pricing](https://www.backblaze.com/cloud-storage/pricing).

AWS entries use first-band on-demand rates per binary GB-month, with separate
Intelligent-Tiering monitoring charges per eligible object-month. B2 uses decimal
GB and a 30-day month. Annual estimates multiply monthly storage by twelve and
assume all currently recorded objects remain unchanged for that period.

Helpers account for per-object minimum billable sizes in IA/Glacier Instant
Retrieval and the 32 KiB archive index plus 8 KiB Standard metadata overhead in
Glacier/Deep Archive. Unknown regions/classes remain unknown rather than borrowing
another region's rate. Intelligent-Tiering uses Frequent Access as a conservative
assumption unless delayed archive access is observed; it cannot infer IA/AIA
residency from a regular HEAD response.

Estimates exclude requests, retrievals, temporary restored copies, egress, tax,
account discounts, free tiers, untracked/pending objects and untracked object versions.
They do not predict future backups or early-deletion charges. No retention or
storage-class changes happen as part of calculating an estimate.
