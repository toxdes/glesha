package backend

import (
	"context"
	"fmt"
	"glesha/backend/aws"
	"glesha/config"
)

type Backend interface {
	CreateResourceContainer(context.Context) error
	UploadResource(context.Context, string) error
}

func GetBackendForProvider(p config.Provider) (Backend, error) {
	switch p {
	case config.PROVIDER_AWS:
		return aws.NewAwsBackend()
	default:
		return nil, fmt.Errorf("no backend found for provider: %s", p.String())
	}
}
