# Releases

Build with the pinned Yesb toolkit; publisher credentials stay in your own `--env` file.
Commit the intended release changes before building so `GIT_SHA` identifies the release revision.

```sh
./yesb/build_all.py
python3 -m unittest discover -s packaging -p 'test_*.py'
./yesb/release_apt.py --dry-run
./yesb/release_rpm.py --dry-run
./packaging/publish_aur.py --dry-run
```

## Direct downloads

Files are published under `https://packages.toxdes.com/glesha/releases/`:

| Platform | Archive |
| --- | --- |
| Linux x64 | `glesha_VERSION_amd64.tar.gz` |
| Linux arm64 | `glesha_VERSION_arm64.tar.gz` |
| macOS Intel | `glesha_VERSION_macos_amd64.tar.gz` |
| macOS Apple Silicon | `glesha_VERSION_macos_arm64.tar.gz` |
| Windows x64 | `glesha_VERSION_windows_amd64.zip` |

Every bundle includes its binary, manual sources, sample configuration, LICENSE and `install.txt`.
Build output includes individual `.sha256` files and `SHA256SUMS`.
The direct publisher uploads the five bundles and their individual checksums.

## Publish

Run these after verifying the build:

```sh
./yesb/release_direct.py --env /path/to/release.env
./yesb/release_apt.py --env /path/to/release.env
./yesb/release_rpm.py --env /path/to/release.env
./packaging/publish_aur.py --env /path/to/release.env --type both
./yesb/release_homebrew.py
```

APT/RPM use the shared toxdes `/apt` and `/rpm` repositories, preserving other apps.
Keep the same signing key used by the existing repositories (`GPG_KEY_ID`).
Production APT/RPM publishing requires signing; validation can run unsigned locally.

Homebrew publishes the `glesha` formula to `toxdes/homebrew-tap`:

```sh
brew install toxdes/tap/glesha
```

The AUR adapter publishes `glesha-bin` and `glesha-git` using Yesb's unchanged
publication helpers. It maps the relocatable bundle into `/usr` for Arch packages.
Use it instead of `yesb/release_aur.py --type bin|both`, whose generic binary
recipe expects a different archive layout. `--type git` and `--type bin` select one package.
The same pinned `.SRCINFO` helper image as vylk is configured; publication needs AUR SSH access.

The macOS and Windows executables are cross-compiled with cgo disabled.
Native execution tests still require those operating systems; macOS bundles are not notarized.

After publishing, merge the release version onto main and rebuild/deploy the website.
Its build reads main's `version.txt` for `/install` and download links.
