package config

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Provider string

func (p *Provider) String() string {
	switch *p {
	case PROVIDER_AWS:
		return "aws"
	default:
		return "Unknown"
	}
}

const (
	PROVIDER_AWS Provider = "aws"
)

func ParseProvider(providerStr string) (Provider, error) {
	p := Provider(strings.ToLower(providerStr))
	switch p {
	case PROVIDER_AWS:
		return PROVIDER_AWS, nil
	default:
		return "", fmt.Errorf("invalid provider: %s", providerStr)
	}
}

func (provider *Provider) UnmarshalJSON(data []byte) error {
	var maybeProvider string
	err := json.Unmarshal(data, &maybeProvider)
	if err != nil {
		return err
	}
	p := Provider(maybeProvider)
	switch p {
	case PROVIDER_AWS:
		{
			*provider = p
			return nil
		}
	default:
		return fmt.Errorf("unknown provier: %s. supported providers are: %s", maybeProvider, PROVIDER_AWS)
	}
}
