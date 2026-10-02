package app_cmd

import (
	"bytes"
	"context"
	"flag"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"glesha/config"
	"glesha/database/repository"
)

func TestDefaultXZArchiveAndPlaintextExtractionWithEmptyPath(t *testing.T) {
	t.Setenv("PATH", "")
	root := t.TempDir()
	source := filepath.Join(root, "docs")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "hello.txt"), []byte("xz content"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "config.toml")
	if err := os.WriteFile(cfg, []byte("[memory]\nmax='128MiB'\n[catalog]\ndirectory='"+filepath.ToSlash(filepath.Join(root, "state"))+"'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	password := filepath.Join(root, "test-password")
	if err := os.WriteFile(password, []byte("local test password"), 0600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "test.tar.xz.gpg")
	ctx := context.Background()
	if err := Execute(ctx, []string{"archive", source, "--output", file, "--passphrase-file", password, "--config", cfg}, "test", "sha"); err != nil {
		t.Fatal(err)
	}
	if err := Execute(ctx, []string{"decrypt", file, "--passphrase-file", password, "--memory-max", "128MiB", "--config", cfg}, "test", "sha"); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "recovered")
	if err := Execute(ctx, []string{"extract", strings.TrimSuffix(file, ".gpg"), "--into", output, "--memory-max", "128MiB", "--config", cfg}, "test", "sha"); err != nil {
		t.Fatal("plaintext xz requested a passphrase", err)
	}
	got, err := os.ReadFile(filepath.Join(output, "test", "docs", "hello.txt"))
	if err != nil || string(got) != "xz content" {
		t.Fatal("xz CLI round trip failed", err)
	}
}

func TestDestinationSyntax(t *testing.T) {
	for _, args := range [][]string{{"--to", "aws,b2"}, {"--to", "aws", "--to", "b2"}, {"--to", "aws,b2", "--to", "aws"}} {
		t.Run(stringsForTest(args), func(t *testing.T) {
			e := &AppCmdEnv{}
			f := flagSet("create", e)
			if err := f.Parse(args); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual([]string(e.To), []string{"aws", "b2"}) {
				t.Fatal(e.To)
			}
		})
	}
	var from destinations
	if err := from.Set("b2,aws"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual([]string(from), []string{"b2", "aws"}) {
		t.Fatal("fallback reordered")
	}
	if err := from.Set("aws,"); err == nil {
		t.Fatal("empty remote accepted")
	}
}
func TestTransferWorkerAliases(t *testing.T) {
	for _, command := range []string{"run", "archive", "import", "restore", "upload", "download", "move", "check"} {
		for _, option := range []string{"--transfer-workers", "--jobs", "-j"} {
			t.Run(command+option, func(t *testing.T) {
				env := &AppCmdEnv{}
				if err := flagSet(command, env).Parse([]string{option, "4"}); err != nil {
					t.Fatal(err)
				}
				if env.TransferWorkers != 4 || env.HashWorkers != 0 {
					t.Fatal("alias did not exclusively set transfer workers", env)
				}
			})
		}
	}
}

func stringsForTest(args []string) string {
	out := ""
	for _, a := range args {
		out += a + " "
	}
	return out
}
func TestCreateIsLocalAndSourceBindingImmutable(t *testing.T) {
	t.Setenv("PATH", "")
	dir := t.TempDir()
	source := filepath.Join(dir, "docs")
	os.Mkdir(source, 0700)
	state := filepath.Join(dir, "state")
	cfg := filepath.Join(dir, "config.toml")
	os.WriteFile(cfg, []byte("[memory]\nmax='128MiB'\n[catalog]\ndirectory='"+filepath.ToSlash(state)+"'\n"), 0600)
	ctx := context.Background()
	if err := Execute(ctx, []string{"create", "docs", source, "--config", cfg}, "test", "sha"); err != nil {
		t.Fatal(err)
	}
	reg, err := repository.NewRegistryRepository(ctx, filepath.Join(state, "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	set, err := reg.Set(ctx, "docs")
	reg.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(set.To) != 0 {
		t.Fatal("implicit destination")
	}
	if set.Compression != "xz" || set.Level != 6 {
		t.Fatal("new set did not use default xz compression")
	}
	if _, err = os.Stat(filepath.Join(state, set.ID, "catalog.db")); !os.IsNotExist(err) {
		t.Fatal("create prepared backup")
	}
	if err = Execute(ctx, []string{"run", "docs", "--config", cfg}, "test", "sha"); err == nil {
		t.Fatal("run accepted implicit destination")
	}
	if err = Execute(ctx, []string{"configure", "docs", "--root", "docs=" + dir, "--config", cfg}, "test", "sha"); err == nil {
		t.Fatal("bound root replaced")
	}
	if err = Execute(ctx, []string{"run", "docs", source, "--config", cfg}, "test", "sha"); err == nil {
		t.Fatal("run accepted source path")
	}
	if err = Execute(ctx, []string{"create", "docs", source, "--config", cfg}, "test", "sha"); err == nil {
		t.Fatal("duplicate set silently replaced")
	}
}

func TestStandaloneCLIArchiveDecryptAndPlainExtract(t *testing.T) {
	t.Setenv("PATH", "")
	dir := t.TempDir()
	source := filepath.Join(dir, "docs")
	os.Mkdir(source, 0700)
	os.WriteFile(filepath.Join(source, "f"), []byte("content"), 0600)
	cfg := filepath.Join(dir, "config.toml")
	os.WriteFile(cfg, []byte("[memory]\nmax='128MiB'\n[catalog]\ndirectory='"+filepath.ToSlash(filepath.Join(dir, "state"))+"'\n"), 0600)
	password := filepath.Join(dir, "test-password.txt")
	os.WriteFile(password, []byte("public test password\n"), 0600)
	ctx := context.Background()
	encrypted := filepath.Join(dir, "encb-test.tar.gz.gpg")
	if err := Execute(ctx, []string{"archive", source, "--compression", "gzip", "--prefix", "encb", "--output", encrypted, "--passphrase-file", password, "--config", cfg}, "test", "sha"); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(dir, "plain.tar.gz")
	if err := Execute(ctx, []string{"decrypt", encrypted, "--output", plain, "--passphrase-file", password, "--config", cfg}, "test", "sha"); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "recovered")
	if err := Execute(ctx, []string{"extract", plain, "--into", out, "--config", cfg}, "test", "sha"); err != nil {
		t.Fatal("plaintext extraction unexpectedly needed a password", err)
	}
	got, err := os.ReadFile(filepath.Join(out, "encb-test", "docs", "f"))
	if err != nil || string(got) != "content" {
		t.Fatal("CLI content lost", err)
	}
	if err := Execute(ctx, []string{"archive", source, "--output", encrypted, "--config", cfg}, "test", "sha"); err == nil {
		t.Fatal("archive output overwritten")
	}
	if err := Execute(ctx, []string{"extract", plain, "--into", out, "--config", cfg}, "test", "sha"); err == nil {
		t.Fatal("extract output overwritten")
	}
}

func TestHelpNeedsNoConfigOrCredentials(t *testing.T) {
	for _, args := range [][]string{
		{"help"}, {"help", "run"}, {"help", "catalog", "pull"},
		{"--config", "/does/not/exist.toml", "help", "archive"},
		{"help", "run", "--color=no"},
	} {
		t.Run(stringsForTest(args), func(t *testing.T) {
			if err := Execute(context.Background(), args, "test", "sha"); err != nil {
				t.Fatal(err)
			}
		})
	}
	if err := Execute(context.Background(), []string{"help", "missing"}, "test", "sha"); err == nil {
		t.Fatal("unknown help command accepted")
	}
}
func TestVersionFormsNeedNoConfigOrCredentials(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"-v"}, {"-version"}, {"--version"}, {"run", "-v"}} {
		t.Run(stringsForTest(args), func(t *testing.T) {
			args = append(args, "--config", "/does/not/exist.toml", "--env", "/does/not/exist.env")
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			defer writer.Close()
			original := os.Stdout
			os.Stdout = writer
			defer func() { os.Stdout = original }()
			err = Execute(context.Background(), args, "1.2.3", "abc123")
			writer.Close()
			os.Stdout = original
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != "glesha 1.2.3 (abc123)\n" {
				t.Fatalf("unexpected version output: %q", data)
			}
		})
	}
	if err := Execute(context.Background(), []string{"version", "extra"}, "test", "sha"); err == nil {
		t.Fatal("unexpected version argument accepted")
	}
}
func TestManualDocumentsEverySupportedFlag(t *testing.T) {
	for command := range commandDescriptions {
		t.Run(command, func(t *testing.T) {
			file := filepath.Join("..", "..", "man", "glesha-"+command+".1")
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			if strings.Count(text, ".TH ") != 1 {
				t.Fatal("manual must contain one page header")
			}
			if !strings.Contains(text, ".SH EXAMPLES") {
				t.Fatal("manual has no examples")
			}
			flagSet(command, &AppCmdEnv{}).VisitAll(func(f *flag.Flag) {
				documented := false
				for _, line := range strings.Split(text, "\n") {
					if !strings.HasPrefix(line, ".B ") {
						continue
					}
					for _, word := range strings.Fields(strings.TrimPrefix(line, ".B ")) {
						word = strings.ReplaceAll(strings.TrimSuffix(word, ","), `\-`, "-")
						if word == "--"+f.Name || word == "-"+f.Name {
							documented = true
						}
					}
				}
				if !documented {
					t.Errorf("undocumented flag --%s", f.Name)
				}
			})
		})
	}
}

func TestHelpIsASCIIAndReadable(t *testing.T) {
	for _, name := range append([]string{""}, "create", "configure", "run", "retry", "list", "status", "history", "browse", "restore", "import", "move", "archive", "decrypt", "extract", "upload", "download", "catalog", "storage-class", "check", "help", "version") {
		text := Usage()
		if name != "" {
			text = CommandUsage(name)
		}
		for _, r := range text {
			if r > 127 {
				t.Fatalf("%s: non-ASCII help", name)
			}
		}
		if strings.Contains(text, "\t") {
			t.Fatalf("%s: tabbed help", name)
		}
		for _, line := range strings.Split(text, "\n") {
			if len(line) > 80 {
				t.Fatalf("%s: line exceeds 80 columns: %s", name, line)
			}
		}
		if name != "" && (!strings.Contains(text, "--env FILE") || !strings.Contains(text, "Global options:")) {
			t.Fatalf("%s: missing global env option", name)
		}
	}
}
func TestCheckContinuesAfterProviderSetupFailure(t *testing.T) {
	for _, key := range []string{"AWS_ACCESS_KEY_ID", "AWS_ACCESS_KEY", "AWS_SECRET_ACCESS_KEY", "AWS_SECRET_KEY", "B2_KEY_ID", "B2_APPLICATION_KEY"} {
		t.Setenv(key, "")
	}
	ctx := context.Background()
	registry, err := repository.NewRegistryRepository(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	cfg := config.Defaults()
	cfg.AWS.Bucket = "test-aws"
	cfg.B2.Bucket = "test-b2"
	cfg.B2.Endpoint = "http://127.0.0.1:1"
	var out bytes.Buffer
	r := &runtime{ctx: ctx, env: &AppCmdEnv{To: destinations{"b2", "aws"}}, config: cfg, registry: registry, root: t.TempDir(), out: &out}
	if err = check(r, nil); err == nil {
		t.Fatal("failed checks succeeded")
	}
	text := out.String()
	if !strings.Contains(text, `Provider "b2"`) || !strings.Contains(text, `Provider "aws"`) || !strings.Contains(text, "Summary: 0 passed, 2 failed.") {
		t.Fatal(text)
	}
	if strings.Count(text, "SKIPPED") != 10 {
		t.Fatal("unattempted checks were not marked skipped", text)
	}
}
