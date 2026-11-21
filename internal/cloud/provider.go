package cloud

import (
	"fmt"
	"os"
	"strings"
)

// Kind names a storage provider. Values are persisted in config and the registry.
type Kind string

const (
	AWS Kind = "aws"
	B2  Kind = "b2"
)

// Capabilities are the provider features that change glesha's behaviour.
type Capabilities struct {
	// ConditionalWrites: create-only puts are atomic, so catalog revisions are
	// published through commits/ instead of heads/.
	ConditionalWrites bool
	// StorageClasses: objects may be stored in a class other than STANDARD.
	StorageClasses bool
	// ColdRetrieval: archive tiers exist and need a restore request before download.
	ColdRetrieval bool
}

// Provider describes one storage provider. It is the only place that knows
// what a provider kind supports.
type Provider struct {
	Kind         Kind
	Capabilities Capabilities
	// SetupHint completes "configure that location before syncing".
	SetupHint string

	// each entry lists the accepted environment names for one value
	keyEnv, secretEnv, tokenEnv []string
	s3                          s3Dialect
}

// s3Dialect holds the differences between S3-compatible APIs.
type s3Dialect struct {
	requireEndpoint  bool
	normalizeRegion  bool
	relaxedChecksums bool
}

var providers = []Provider{
	{
		Kind:         AWS,
		Capabilities: Capabilities{ConditionalWrites: true, StorageClasses: true, ColdRetrieval: true},
		SetupHint:    " in [aws]/[remotes.NAME], or use AWS_BUCKET_NAME and AWS_REGION",
		keyEnv:       []string{"AWS_ACCESS_KEY_ID", "AWS_ACCESS_KEY"},
		secretEnv:    []string{"AWS_SECRET_ACCESS_KEY", "AWS_SECRET_KEY"},
		tokenEnv:     []string{"AWS_SESSION_TOKEN"},
		s3:           s3Dialect{normalizeRegion: true},
	},
	{
		Kind:      B2,
		SetupHint: " in [b2]/[remotes.NAME], or use B2_BUCKET_NAME and B2_BUCKET_ENDPOINT",
		keyEnv:    []string{"B2_KEY_ID"},
		secretEnv: []string{"B2_APPLICATION_KEY"},
		s3:        s3Dialect{requireEndpoint: true, relaxedChecksums: true},
	},
}

// Lookup returns the provider for a persisted kind name.
func Lookup(kind string) (Provider, error) {
	for _, p := range providers {
		if string(p.Kind) == kind {
			return p, nil
		}
	}
	return Provider{}, fmt.Errorf("cloud: unknown provider %q", kind)
}

// Supports reports whether kind names a known provider.
func Supports(kind string) bool {
	_, err := Lookup(kind)
	return err == nil
}

// ValidateCredentials reports which required credentials are missing from the environment.
func (p Provider) ValidateCredentials() error {
	var missing []string
	for _, names := range [][]string{p.keyEnv, p.secretEnv} {
		if firstEnv(names...) != "" {
			continue
		}
		name := names[0]
		if len(names) > 1 {
			name += " (or " + strings.Join(names[1:], " or ") + ")"
		}
		missing = append(missing, name)
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("cloud: %s credentials are missing: %s; set them in the environment or load them with --env FILE", p.Kind, strings.Join(missing, ", "))
}

func (p Provider) credentials() (key, secret, token string) {
	return firstEnv(p.keyEnv...), firstEnv(p.secretEnv...), firstEnv(p.tokenEnv...)
}

// CapabilitiesOf returns the capabilities of kind; an unknown kind supports nothing.
func CapabilitiesOf(kind string) Capabilities {
	p, _ := Lookup(kind)
	return p.Capabilities
}

// firstEnv returns the first non-empty environment variable among keys.
func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}
