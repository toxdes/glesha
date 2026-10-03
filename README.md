# glesha

Encrypted archives and backups to AWS S3, Backblaze B2 and compatible S3 endpoints.
One standalone executable.

[Website](https://glesha.toxdes.com) · [Docs](https://glesha.toxdes.com/docs.html) · [CLI reference](cli_reference.md)

- Full and incremental snapshots, with a browsable file catalog.
- XZ compression by default; gzip and bzip2 also supported.
- Chunked backups when a complete archive won't fit on local disk.
- AWS storage-class changes and explicit cold-storage retrieval.
- Existing encb archives stay readable without conversion. Single-file full archives can also be recovered with GPG and tar.

## Install

Linux amd64 and arm64:

```sh
curl -fsSL https://glesha.toxdes.com/install | sh
```

See [installation options](https://glesha.toxdes.com/docs.html#installation) for binary downloads and manual installation.

## Back up and restore

Configure [provider credentials](https://glesha.toxdes.com/configuration.html) in your environment or pass `--env FILE`.

```sh
glesha create docs ~/Documents --to b2
glesha run docs
glesha run docs --incremental
glesha history docs
glesha browse docs --path Documents --recursive
glesha restore docs --into ~/recovered
```

`create` saves the set's settings. `run` creates a full snapshot unless you pass `--incremental`.

For limited local disk space:

```sh
glesha run docs --chunked --spool-max 128M
```

To create a local archive without a backup set:

```sh
glesha archive ~/Documents
```

Use `glesha help COMMAND` or `man glesha` for options and examples.
See the [guides](https://glesha.toxdes.com/guides.html), [sample configuration](config-sample.toml) and [compatibility notes](compatibility.md) for more.

## Development

Go 1.25 or newer:

```sh
CGO_ENABLED=0 go build -o build/glesha .
./build/glesha help
go test ./...
go test -race ./...
```

Release packages are built with [Yesb](https://github.com/toxdes/yesb): `./yesb/build_all.py`.

## License

[MIT](LICENSE)
