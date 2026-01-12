# Glesha

[`glesha`](https://glesha.txds.me) is a lightweight CLI tool written in Go for archiving files or directories into `.tar.xz` format, store files metadata in SQLite, and uploading the archives to AWS S3. It is optimized for resilient, resumable uploads and long-term cold storage.

# Installation Instructions
Check [install instructions](https://glesha.txds.me/docs.html#installation) or alternatively [download latest binary](https://github.com/toxdes/glesha/releases/latest) from releases and add it to your `PATH` environment.

# Build Instructions
```cmd
git clone https://github.com/toxdes/glesha.git
```
```cmd
cd glesha
```
```cmd
./scripts/build.sh
```

# Usage

```cmd
$ glesha
USAGE
   glesha [-v | -version] [-h | -help] <command> [<args>]

   DESCRIPTION
   glesha is a cross-platform archive and upload utility.

   COMMANDS
   These are common glesha commands used in various situations -
   help       Help about a subcommand
   add        Creates a glesha archive and upload task
   run        Runs a glesha task
   tui        Interactive terminal user interface
   ls         Lists all available glesha tasks
   rm         Deletes a glesha task, and relevant cache files
   cleanup    Cleans up cache, unwanted files created by glesha.

   EXAMPLES
   See 'glesha help <command>' to read about a specific subcommand.

   SEE ALSO
   1. glesha help add
   2. glesha help ls
```

# Configuration

```json
{
	"archive_format": "targz",
	"provider": "aws",
	"aws": {
		"account_id": 0,
		"access_key": "YOUR_ACCESS_KEY",
		"secret_key": "YOUR_SECRET_KEY",
		"region": "ap-south-1",
		"bucket_name": "YOUR_BUCKET_NAME",
		"storage_class": "<aws-storage-class>"
	}
}
```

---

# Roadmap

- [x] Archive directories/files into compressed `.tar.xz` format
- [x] Store files metadata storage using SQLite
- [x] Multipart concurrent uploads to AWS Glacier Deep Archive
- [x] Resumable upload support for flaky networks
- [ ] Basic TUI
- [ ] `glesha config ...` for editing config from CLI
- [ ] `glesha ls` for listing tasks
- [ ] `glesha sync` for incremental backup of the same task
- [ ] `glesha update` for self-updating binary
- [ ] Generate `man` pages

