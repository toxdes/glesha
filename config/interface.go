package config

// Configurator is an interface that wraps the config package's functions.
// It is used to allow mocking the config package in tests.
type Configurator interface {
	GetDefaultConfigPath() (string, error)
	Parse(string) error
	Get() *Config
}

// New returns a new Configurator.
func New() Configurator {
	return &defaultConfigurator{}
}

type defaultConfigurator struct{}

func (c *defaultConfigurator) GetDefaultConfigPath() (string, error) {
	return GetDefaultConfigPath()
}

func (c *defaultConfigurator) Parse(path string) error {
	return Parse(path)
}

func (c *defaultConfigurator) Get() *Config {
	return Get()
}
