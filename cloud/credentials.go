package cloud

import (
	"fmt"
	"strings"

	"glesha/config"
)

func ValidateCredentials(provider string) error {
	var required [][]string
	switch provider {
	case "aws":
		required = [][]string{{"AWS_ACCESS_KEY_ID", "AWS_ACCESS_KEY"}, {"AWS_SECRET_ACCESS_KEY", "AWS_SECRET_KEY"}}
	case "b2":
		required = [][]string{{"B2_KEY_ID"}, {"B2_APPLICATION_KEY"}}
	default:
		return fmt.Errorf("cloud: unknown provider %q", provider)
	}
	var missing []string
	for _, names := range required {
		if config.FirstEnv(names...) != "" {
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
	return fmt.Errorf("cloud: %s credentials are missing: %s; set them in the environment or load them with --env FILE", provider, strings.Join(missing, ", "))
}
