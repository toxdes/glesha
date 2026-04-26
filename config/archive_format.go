package config

import (
	"encoding/json"
	"fmt"
	"strings"
)

type ArchiveFormat string

const (
	AF_TARGZ ArchiveFormat = "targz"
	AF_ZIP   ArchiveFormat = "zip"
)

func (archiveFormat *ArchiveFormat) String() string {
	switch *archiveFormat {
	case AF_TARGZ:
		return ".tar.gz"
	case AF_ZIP:
		return ".zip"
	default:
		return "Unknown"
	}
}

func ParseArchiveFormat(archiveFormatStr string) (ArchiveFormat, error) {
	a := ArchiveFormat(strings.ToLower(archiveFormatStr))
	switch a {
	case AF_TARGZ:
		return AF_TARGZ, nil
	default:
		return "", fmt.Errorf("invalid archive format: %s", archiveFormatStr)
	}
}

func (archiveFormat *ArchiveFormat) UnmarshalJSON(data []byte) error {
	var maybeArchiveFormat string
	err := json.Unmarshal(data, &maybeArchiveFormat)
	if err != nil {
		return err
	}
	t := ArchiveFormat(maybeArchiveFormat)
	if t == AF_TARGZ {
		*archiveFormat = t
		return nil
	}
	return fmt.Errorf("unknown archive_type: %s. supported archive types: %s", maybeArchiveFormat, AF_TARGZ)
}
