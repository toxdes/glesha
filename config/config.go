package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"glesha/file_io"
)

type Config struct {
	Remotes map[string]Provider `toml:"remotes"`
	Archive struct {
		Prefix      string `toml:"prefix"`
		Compression string `toml:"compression"`
		Level       int    `toml:"compression_level"`
		Mode        string `toml:"mode"`
	} `toml:"archive"`
	Memory struct {
		Max string `toml:"max"`
	} `toml:"memory"`
	Workers struct {
		Hash     int `toml:"hash"`
		Transfer int `toml:"transfer"`
	} `toml:"workers"`
	Backup struct {
		FullClass        string `toml:"full_storage_class"`
		IncrementalClass string `toml:"incremental_storage_class"`
	} `toml:"backup"`
	AWS     Provider `toml:"aws"`
	B2      Provider `toml:"b2"`
	Catalog struct {
		Encrypted bool   `toml:"encrypted"`
		Directory string `toml:"directory"`
		Prefix    string `toml:"prefix"`
	} `toml:"catalog"`
	Cold struct {
		Days int32  `toml:"days"`
		Tier string `toml:"tier"`
	} `toml:"cold"`
}
type Provider struct {
	Kind         string `toml:"kind"`
	Bucket       string `toml:"bucket"`
	Region       string `toml:"region"`
	Endpoint     string `toml:"endpoint"`
	StorageClass string `toml:"storage_class"`
}

func DefaultPath() (string, error) {
	d, e := os.UserConfigDir()
	return filepath.Join(d, "glesha", "config.toml"), e
}
func Defaults() Config {
	var c Config
	c.Archive.Prefix = "glesha"
	c.Archive.Compression = "xz"
	c.Archive.Level = 6
	c.Archive.Mode = "auto"
	c.Workers.Hash = 4
	c.Workers.Transfer = 4
	c.AWS.Region = ""
	c.AWS.StorageClass = ""
	c.Catalog.Encrypted = true
	c.Catalog.Prefix = "glesha/v2"
	c.Cold.Days = 7
	c.Cold.Tier = "Bulk"
	return c
}
func Load(p string) (Config, error) {
	c := Defaults()
	explicit := p != ""
	var e error
	if p == "" {
		p, e = DefaultPath()
	} else {
		p, e = file_io.Expand(p)
	}
	if e != nil {
		return c, e
	}
	b, e := os.ReadFile(p)
	if os.IsNotExist(e) && !explicit {
		return c, nil
	}
	if e != nil {
		return c, e
	}
	if e = toml.Unmarshal(b, &c); e != nil {
		return c, fmt.Errorf("config: parse TOML: %w", e)
	}
	return c, nil
}
func (c Config) Validate() error {
	if c.Archive.Prefix != "glesha" && c.Archive.Prefix != "encb" {
		return fmt.Errorf("config: prefix must be glesha or encb")
	}
	if c.Archive.Compression != "gzip" && c.Archive.Compression != "bzip2" && c.Archive.Compression != "xz" {
		return fmt.Errorf("config: compression must be xz, gzip or bzip2")
	}
	if c.Archive.Level < 1 || c.Archive.Level > 9 {
		return fmt.Errorf("config: compression level must be 1..9")
	}
	switch c.Archive.Mode {
	case "auto", "memory", "stream":
	default:
		return fmt.Errorf("config: archive mode must be auto, memory or stream")
	}
	if c.Workers.Hash < 1 || c.Workers.Transfer < 1 {
		return fmt.Errorf("config: worker counts must be positive")
	}
	switch strings.ToLower(c.Cold.Tier) {
	case "bulk", "standard", "expedited":
	default:
		return fmt.Errorf("config: unsupported retrieval tier")
	}
	if c.Cold.Days < 1 {
		return fmt.Errorf("config: cold days must be positive")
	}
	return nil
}
func Env(p string) error {
	if p == "" {
		return nil
	}
	p, e := file_io.Expand(p)
	if e != nil {
		return e
	}
	f, e := os.Open(p)
	if e != nil {
		return e
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return fmt.Errorf("config: invalid environment assignment")
		}
		v = strings.Trim(strings.TrimSpace(v), "\"'")
		if _, exists := os.LookupEnv(k); !exists {
			if e = os.Setenv(k, v); e != nil {
				return e
			}
		}
	}
	return s.Err()
}
func FirstEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}
func (c *Config) ProviderEnv() {
	if c.AWS.Bucket == "" {
		c.AWS.Bucket = FirstEnv("AWS_BUCKET_NAME")
	}
	if v := FirstEnv("AWS_REGION"); v != "" && c.AWS.Region == "" {
		c.AWS.Region = v
	}
	if v := FirstEnv("AWS_STORAGE_CLASS"); v != "" && c.AWS.StorageClass == "" {
		c.AWS.StorageClass = v
	}
	if c.AWS.Region == "" {
		c.AWS.Region = "us-east-1"
	}
	if c.AWS.StorageClass == "" {
		c.AWS.StorageClass = "STANDARD"
	}
	if c.B2.Bucket == "" {
		c.B2.Bucket = FirstEnv("B2_BUCKET_NAME")
	}
	if c.B2.Endpoint == "" {
		c.B2.Endpoint = FirstEnv("B2_BUCKET_ENDPOINT")
	}
	if c.B2.Endpoint != "" && !strings.Contains(c.B2.Endpoint, "://") {
		c.B2.Endpoint = "https://" + c.B2.Endpoint
	}
	if c.B2.Region == "" {
		host := strings.TrimPrefix(c.B2.Endpoint, "https://")
		parts := strings.Split(host, ".")
		if len(parts) > 1 {
			c.B2.Region = parts[1]
		}
	}
}

func (c Config) RemotesByName() map[string]Provider {
	out := make(map[string]Provider, len(c.Remotes)+2)
	for name, p := range c.Remotes {
		out[name] = p
	}
	if _, ok := out["aws"]; !ok {
		p := c.AWS
		p.Kind = "aws"
		out["aws"] = p
	}
	if _, ok := out["b2"]; !ok {
		p := c.B2
		p.Kind = "b2"
		out["b2"] = p
	}
	return out
}
