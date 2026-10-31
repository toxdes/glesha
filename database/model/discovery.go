package model

const DiscoverySchema = `
CREATE TABLE IF NOT EXISTS discovery(id TEXT PRIMARY KEY,set_id TEXT NOT NULL,data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS discovery_replicas(locator TEXT NOT NULL,remote TEXT NOT NULL,PRIMARY KEY(locator,remote));
`

// Locator contains no set names, source paths, or archive contents.
type Locator struct {
	Version    DiscoveryVersion `json:"version"`
	Set        string           `json:"set"`
	Authority  Remote           `json:"authority"`
	Prefix     string           `json:"prefix"`
	Parent     string           `json:"parent,omitempty"`
	Supersedes []string         `json:"supersedes,omitempty"`
}
