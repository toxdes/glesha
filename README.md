# Glesha

`glesha` is a lightweight CLI tool written in Go for archiving files or directories into `.tar.xz` format, storing encrypted metadata in SQLite, and uploading the archives to AWS Glacier Deep Archive. It is optimized for resilient, resumable uploads and long-term cold storage.

---

## 🚀 Features (TODO)

- Archive directories/files into compressed `.tar.xz` format
- Encrypted metadata storage using SQLite
- Multipart concurrent uploads to AWS Glacier Deep Archive
- Resumable upload support for flaky networks
- Clean, modular Go project structure

---

## Build Instructions

```bash
git clone https://github.com/toxdes/glesha.git
cd glesha
./build.sh
```

---

## Usage

Download [latest binary](https://github.com/toxdes/glesha/releases/latest) from releases, add it to your environment PATH.

```cmd
$ glesha
--config string
        Path to config.json file (required)
--dir string
        Path to file or directory to archive (required)
```

---

## Configuration

```json
{
  "aws_access_key": "YOUR_ACCESS_KEY",
  "aws_secret_key": "YOUR_SECRET_KEY",
  "region": "ap-south-1",
  "bucket_name": "your-bucket-name"
}
```

---
