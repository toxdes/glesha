package backup

import "fmt"

type CatalogState string

const (
	CatalogStateDownloaded CatalogState = "DOWNLOADED"
	CatalogStateLocked     CatalogState = "LOCKED"
	CatalogStateSynced     CatalogState = "SYNCED"
	CatalogStateFailed     CatalogState = "FAILED"
	CatalogStateSkipped    CatalogState = "SKIPPED"
)

func (s CatalogState) String() string { return string(s) }

func ParseCatalogState(value string) (CatalogState, error) {
	s := CatalogState(value)
	switch s {
	case CatalogStateDownloaded, CatalogStateLocked, CatalogStateSynced, CatalogStateFailed, CatalogStateSkipped:
		return s, nil
	default:
		return "", fmt.Errorf("backup: unknown catalog state %q", value)
	}
}
