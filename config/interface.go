package config

type Configurator interface{ Load(string) (Config, error) }
