package backend

import (
	"fmt"
	"glesha/backend/aws"
	"glesha/config"
)

type Backend interface {
	CreateResourceContainer() error
	UploadResource(resourceFilePath string) error
}

func GetBackendForProvider(p config.Provider) (Backend, error) {
	switch p {
	case config.PROVIDER_AWS:
		return aws.NewAwsBackend()
	default:
		return nil, fmt.Errorf("No backend found for provider: %s", p.String())
	}
}
