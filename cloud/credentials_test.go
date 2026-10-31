package cloud

import (
	"context"
	"strings"
	"testing"

	"glesha/config"
)

func TestCredentialRequirements(t *testing.T) {
	for _, tc := range []struct {
		name, provider string
		env            map[string]string
		missing        []string
		present        []string
	}{
		{name: "aws missing", provider: "aws", missing: []string{"AWS_ACCESS_KEY_ID", "AWS_ACCESS_KEY", "AWS_SECRET_ACCESS_KEY", "AWS_SECRET_KEY"}},
		{name: "aws key present", provider: "aws", env: map[string]string{"AWS_ACCESS_KEY_ID": "credential-value"}, missing: []string{"AWS_SECRET_ACCESS_KEY", "AWS_SECRET_KEY"}, present: []string{"AWS_ACCESS_KEY_ID", "AWS_ACCESS_KEY"}},
		{name: "aws secret alias present", provider: "aws", env: map[string]string{"AWS_SECRET_KEY": "credential-value"}, missing: []string{"AWS_ACCESS_KEY_ID", "AWS_ACCESS_KEY"}, present: []string{"AWS_SECRET_ACCESS_KEY", "AWS_SECRET_KEY"}},
		{name: "aws aliases complete", provider: "aws", env: map[string]string{"AWS_ACCESS_KEY": "credential-value", "AWS_SECRET_KEY": "credential-value"}},
		{name: "aws complete without session token", provider: "aws", env: map[string]string{"AWS_ACCESS_KEY_ID": "credential-value", "AWS_SECRET_ACCESS_KEY": "credential-value"}},
		{name: "b2 missing", provider: "b2", missing: []string{"B2_KEY_ID", "B2_APPLICATION_KEY"}},
		{name: "b2 key present", provider: "b2", env: map[string]string{"B2_KEY_ID": "credential-value"}, missing: []string{"B2_APPLICATION_KEY"}, present: []string{"B2_KEY_ID"}},
		{name: "b2 cannot use aws credentials", provider: "b2", env: map[string]string{"AWS_ACCESS_KEY_ID": "credential-value", "AWS_SECRET_ACCESS_KEY": "credential-value"}, missing: []string{"B2_KEY_ID", "B2_APPLICATION_KEY"}},
		{name: "b2 complete", provider: "b2", env: map[string]string{"B2_KEY_ID": "credential-value", "B2_APPLICATION_KEY": "credential-value"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, name := range []string{"AWS_ACCESS_KEY_ID", "AWS_ACCESS_KEY", "AWS_SECRET_ACCESS_KEY", "AWS_SECRET_KEY", "AWS_SESSION_TOKEN", "B2_KEY_ID", "B2_APPLICATION_KEY"} {
				t.Setenv(name, tc.env[name])
			}
			err := ValidateCredentials(tc.provider)
			_, setupErr := New(context.Background(), tc.provider, config.Provider{Bucket: "bucket", Endpoint: "http://127.0.0.1:1", Region: "us-east-1"}, 1, nil)
			if len(tc.missing) == 0 {
				if err != nil || setupErr != nil {
					t.Fatalf("valid credentials rejected: %v, %v", err, setupErr)
				}
				return
			}
			if err == nil || setupErr == nil || err.Error() != setupErr.Error() {
				t.Fatalf("credential checks disagree: %v, %v", err, setupErr)
			}
			message := err.Error()
			for _, name := range append(tc.missing, "--env FILE") {
				if !strings.Contains(message, name) {
					t.Fatalf("missing guidance for %s: %s", name, message)
				}
			}
			for _, name := range append(tc.present, "credential-value", "AWS_SESSION_TOKEN") {
				if strings.Contains(message, name) {
					t.Fatalf("reported a supplied, secret or optional value: %s", message)
				}
			}
		})
	}
}
