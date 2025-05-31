package backend

import (
	"fmt"
	"glesha/backend/aws"
	"glesha/cmd"
)

type Backend interface {
	CreateResourceContainer() error
	UploadResource(resourceFilePath string) error
}

func GetBackendForProvider(p cmd.Provider) (Backend, error) {
	switch p {
	case cmd.ProviderAws:
		return aws.NewAwsBackend()
	default:
		return nil, fmt.Errorf("No backend found for provider: %s", p.String())
	}
}
