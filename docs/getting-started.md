# Getting started

glesha is a single executable that creates encrypted archives and stores them in AWS S3 or Backblaze B2. A local catalog records every backup, so you can list files without downloading archives.

## Installation

### Linux

#### Debian and Ubuntu

Add the toxdes APT repository, then install the package:

```sh
curl -fsSL https://packages.toxdes.com/apt/pubkey.gpg | sudo gpg --dearmor -o /usr/share/keyrings/toxdes.gpg
echo 'deb [signed-by=/usr/share/keyrings/toxdes.gpg] https://packages.toxdes.com/apt stable main' | sudo tee /etc/apt/sources.list.d/toxdes.list
sudo apt update
sudo apt install glesha
```

If the repository is already configured, run only `sudo apt install glesha`.

#### Fedora and RHEL

Add the toxdes RPM repository, then install the package:

```sh
sudo tee /etc/yum.repos.d/toxdes.repo <<'EOF'
[toxdes]
name=Toxdes
baseurl=https://packages.toxdes.com/rpm
enabled=1
gpgcheck=1
repo_gpgcheck=1
gpgkey=https://packages.toxdes.com/rpm/pubkey.gpg
EOF
sudo dnf install glesha
```

#### Arch Linux

Install the prebuilt package from the AUR:

```sh
yay -S glesha-bin
```

#### Prebuilt binaries

Each bundle contains the executable, the manual pages, and `install.txt`, which lists the installation steps.

| Architecture | Bundle | Checksum |
| --- | --- | --- |
| x64 | [glesha_@@VERSION@@_amd64.tar.gz](@@RELEASE_URL@@/glesha_@@VERSION@@_amd64.tar.gz) | [SHA-256](@@RELEASE_URL@@/glesha_@@VERSION@@_amd64.tar.gz.sha256) |
| arm64 | [glesha_@@VERSION@@_arm64.tar.gz](@@RELEASE_URL@@/glesha_@@VERSION@@_arm64.tar.gz) | [SHA-256](@@RELEASE_URL@@/glesha_@@VERSION@@_arm64.tar.gz.sha256) |

#### Install script

Alternatively, run the install script:

```sh
curl -fsSL https://glesha.toxdes.com/install | sh
```

The script installs `glesha` in `~/.local/bin`. If `~/.local/bin` is not on your `PATH`, add it in your shell profile. Check [script options](#install-script-options) if you want to modify the installation, or want to uninstall.

### macOS

Install with Homebrew:

```sh
brew install toxdes/tap/glesha
```

Without Homebrew, run the install script:

```sh
curl -fsSL https://glesha.toxdes.com/install | sh
```

The script installs `glesha` in `~/.local/bin`. If `~/.local/bin` is not on your `PATH`, add it in your shell profile. Check [script options](#install-script-options) if you want to modify the installation, or want to uninstall.


### Windows

1. Download [glesha_@@VERSION@@_windows_amd64.zip](@@RELEASE_URL@@/glesha_@@VERSION@@_windows_amd64.zip) and its [checksum](@@RELEASE_URL@@/glesha_@@VERSION@@_windows_amd64.zip.sha256).
2. Compare the checksum. The two hashes must match:

	```powershell
	Get-FileHash .\glesha_@@VERSION@@_windows_amd64.zip -Algorithm SHA256
	Get-Content .\glesha_@@VERSION@@_windows_amd64.zip.sha256
	```

3. Extract the bundle:

	```powershell
	Expand-Archive .\glesha_@@VERSION@@_windows_amd64.zip -DestinationPath .\glesha
	```

4. Add the directory that contains `bin\glesha.exe` to `PATH`.

### Other systems

#### Manual downloads

Each bundle contains the executable, the manual pages, and `install.txt`. Each bundle has a `.sha256` checksum file at the same URL with `.sha256` appended.

| System | Bundle |
| --- | --- |
| Linux x64 | [glesha_@@VERSION@@_amd64.tar.gz](@@RELEASE_URL@@/glesha_@@VERSION@@_amd64.tar.gz) |
| Linux arm64 | [glesha_@@VERSION@@_arm64.tar.gz](@@RELEASE_URL@@/glesha_@@VERSION@@_arm64.tar.gz) |
| macOS Intel | [glesha_@@VERSION@@_macos_amd64.tar.gz](@@RELEASE_URL@@/glesha_@@VERSION@@_macos_amd64.tar.gz) |
| macOS Apple Silicon | [glesha_@@VERSION@@_macos_arm64.tar.gz](@@RELEASE_URL@@/glesha_@@VERSION@@_macos_arm64.tar.gz) |
| Windows x64 | [glesha_@@VERSION@@_windows_amd64.zip](@@RELEASE_URL@@/glesha_@@VERSION@@_windows_amd64.zip) |

#### Build from source

You need Go 1.25 or newer.

```sh
git clone https://github.com/toxdes/glesha.git
cd glesha
CGO_ENABLED=0 go build -o build/glesha .
./build/glesha version
```

### Check the installation

```sh
glesha version
```

The output has the form `glesha @@VERSION@@ (1a2b3c4)`.

### Install script options

To install a specific version, pass `--version`:

```sh
curl -fsSL https://glesha.toxdes.com/install | sh -s -- --version @@VERSION@@
```

macOS builds and package-manager releases start at version 0.5.1.

To install into another prefix, pass `--prefix` or set `INSTALL_PREFIX`. `--prefix` takes precedence.

```sh
curl -fsSL https://glesha.toxdes.com/install | INSTALL_PREFIX="$HOME/opt" sh
```

To install for all users, download the script first and run it with `sudo`:

```sh
curl -fsSL https://glesha.toxdes.com/install -o /tmp/glesha-install
sudo sh /tmp/glesha-install --prefix /usr/local
```

### Uninstall

For a script installation, run the script with `--uninstall` and the same `--prefix` that you used to install.

```sh
curl -fsSL https://glesha.toxdes.com/install | sh -s -- --uninstall
```

The script asks for confirmation, then removes the executable and the manual pages. Configuration, local state, and cloud backups are not removed.

For a package installation, use the package manager.

## Back up your first directory

This tutorial backs up `~/Documents` to Backblaze B2, makes an incremental backup, lists the backed-up files, and restores them.

You need:

- glesha, from [Installation](#installation).
- A Backblaze B2 bucket.
- A B2 application key with read and write access to that bucket.

The bucket page in the B2 console shows the bucket endpoint, for example `s3.us-west-004.backblazeb2.com`.

### Step 1: Save your credentials

Create `~/.config/glesha/cloud.env` with your values:

```dotenv
B2_KEY_ID=your-key-id
B2_APPLICATION_KEY=your-application-key
B2_BUCKET_NAME=your-bucket
B2_BUCKET_ENDPOINT=https://s3.us-west-004.backblazeb2.com
```

Restrict access to the file:

```sh
chmod 600 ~/.config/glesha/cloud.env
```

glesha reads this file only when you pass `--env`.

### Step 2: Check bucket access

```sh
glesha check --to b2 --env ~/.config/glesha/cloud.env
```

glesha writes, reads, and deletes a test object. The output shows one result for each step:

```text
Provider "b2" (b2)
  Safety     OK
  Write      OK
  Metadata   OK
  Read       OK
  Cleanup    OK
  Result      PASS

Summary: 1 passed, 0 failed.
```

A `FAILED` line includes the error. Correct the credentials or the bucket permissions before you continue.

### Step 3: Create a backup set

A backup set is a named list of source directories and destinations. Create a set named `docs`:

```sh
glesha create docs ~/Documents --to b2 --env ~/.config/glesha/cloud.env
```

Enter a new catalog passphrase twice. The catalog passphrase encrypts the file list that glesha stores in the bucket. glesha does not store the passphrase.

Output:

```text
Created "docs".
```

`create` does not back up files. It saves the set and publishes an empty catalog.

### Step 4: Run the first backup

```sh
glesha run docs --env ~/.config/glesha/cloud.env
```

Enter the catalog passphrase, then a new archive passphrase twice. The archive passphrase encrypts your files. Use the same archive passphrase for every run of the set.

To skip the catalog prompt, add `GLESHA_CATALOG_PASSPHRASE` to the credentials file. [Passphrases](configuration.md#passphrases) lists the other sources.

The last line of the output gives the snapshot ID:

```text
Created snapshot 7313fb03-1fab-48d6-89ee-9e10adb21b70 of "docs".
```

A snapshot is one backup of the set at one point in time.

### Step 5: Back up only the changes

Change a file in `~/Documents`, then run an incremental backup:

```sh
glesha run docs --incremental --env ~/.config/glesha/cloud.env
```

The new archive contains only the changed files.

If you deleted a file, glesha lists the removed paths and asks for confirmation. Enter `y` to record the deletion. Older snapshots keep the file.

If no file changed, the output is:

```text
No changes in "docs"; no snapshot created.
```

### Step 6: List snapshots and files

List the snapshots, newest first:

```sh
glesha history docs
```

```text
SNAPSHOT                              CREATED               TYPE               SIZE  COPIES
b0e70271-72a4-4c79-a6fa-a9cf03eea029  2026-11-21 12:09 IST  incremental        843B  b2: readable (STANDARD)
7313fb03-1fab-48d6-89ee-9e10adb21b70  2026-11-21 12:09 IST  full               847B  b2: readable (STANDARD)
```

List every file in the latest snapshot:

```sh
glesha browse docs --recursive
```

```text
TYPE              SIZE  PATH
directory            -  Documents
directory            -  Documents/notes
file                8B  Documents/c.txt
file                6B  Documents/notes/a.txt
```

Paths start with the source directory name, `Documents`. `history` and `browse` read the local catalog and make no cloud requests.

### Step 7: Restore the files

Restore the latest snapshot:

```sh
glesha restore docs --into ~/recovered --env ~/.config/glesha/cloud.env
```

Enter the archive passphrase. Output:

```text
Restored "docs" to "/home/you/recovered".
```

The files are in `~/recovered/Documents`. If a destination file already exists, the restore stops before it downloads anything. glesha never overwrites files.

### Step 8: Check the set

```sh
glesha status
```

```text
SET    STATE   SIZE   SNAPSHOTS  STORAGE CLASS  EST/YR
docs   SYNCED  1.65K  2          B2             <$0.01
TOTAL          1.65K  2                         <$0.01
```

`SYNCED` means the set has no unfinished work. `EST/YR` is the estimated yearly storage cost.

### Next steps

- [Concepts](concepts.md) describes sets, snapshots, catalogs, and passphrases.
- [Configuration](configuration.md) lists every setting.
- [How-to guides](guides.md) cover scheduled backups, file recovery, cold storage, and recovery on a new computer.
- The [API reference](cli-reference.md) describes every command. `glesha help COMMAND` shows the same information.
