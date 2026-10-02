package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"glesha/cloud"
	"glesha/pricing"
)

// this local cache is advisory; archive and catalog formats remain unchanged
type MetadataStatus struct {
	Version    int                      `json:"version"`
	Remote     string                   `json:"remote"`
	Revision   string                   `json:"revision,omitempty"`
	Number     int64                    `json:"number,omitempty"`
	Bytes      int64                    `json:"bytes"`
	Objects    int64                    `json:"objects"`
	Complete   bool                     `json:"complete"`
	AsOf       time.Time                `json:"as_of"`
	LastUpload *time.Time               `json:"last_upload,omitempty"`
	Classes    map[string]MetadataClass `json:"classes,omitempty"`
}

type MetadataClass struct {
	Bytes            int64 `json:"bytes"`
	Objects          int64 `json:"objects"`
	BillableBytes    int64 `json:"billable_bytes"`
	MonitoredObjects int64 `json:"monitored_objects"`
}

func (s *Service) MetadataStatus() (MetadataStatus, error) {
	var v MetadataStatus
	b, err := os.ReadFile(filepath.Join(s.Directory, "metadata-status.json"))
	if os.IsNotExist(err) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	if err = json.Unmarshal(b, &v); err != nil {
		return v, err
	}
	if v.Version != 1 || v.Bytes < 0 || v.Objects < 0 {
		return v, fmt.Errorf("backup: unsupported or invalid metadata status cache")
	}
	if v.Remote != s.Set.CatalogTo {
		return MetadataStatus{}, nil
	}
	return v, nil
}

func (s *Service) RefreshMetadataStatus(ctx context.Context) error {
	if _, err := s.MetadataStatus(); err != nil {
		return err
	}
	store, err := s.metadataStore()
	if err != nil || store == nil {
		return err
	}
	v := MetadataStatus{Version: 1, Remote: s.Set.CatalogTo, Complete: true, AsOf: now(), Classes: map[string]MetadataClass{}}
	if err = cloud.List(ctx, store, s.prefix(), func(o cloud.Object) error {
		if o.Size < 0 {
			return fmt.Errorf("backup: negative remote metadata size")
		}
		v.Bytes += o.Size
		v.Objects++
		class := o.Class
		if class == "" {
			class = "STANDARD"
		}
		group := v.Classes[class]
		group.Bytes += o.Size
		group.Objects++
		group.BillableBytes += pricing.BillableBytes(class, o.Size)
		if o.Size >= pricing.MinimumIABytes {
			group.MonitoredObjects++
		}
		v.Classes[class] = group
		if o.ModifiedAt != nil && (v.LastUpload == nil || o.ModifiedAt.After(*v.LastUpload)) {
			t := *o.ModifiedAt
			v.LastUpload = &t
		}
		return nil
	}); err != nil {
		return err
	}
	return s.saveJob(filepath.Join(s.Directory, "metadata-status.json"), v)
}

func (s *Service) recordMetadataPublication(v revision) error {
	file := filepath.Join(s.Directory, "publication-"+v.ID)
	info, err := os.Stat(file)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	descriptor, err := os.Stat(file + ".descriptor")
	if err != nil {
		return err
	}
	status, err := s.MetadataStatus()
	if err != nil {
		return err
	}
	if status.Revision == v.ID {
		return nil
	}
	// a listing may contain pending proposals; do not count them again on publication
	if status.Version != 0 && status.Number == 0 {
		status = MetadataStatus{Version: 1, Remote: s.Set.CatalogTo}
	}
	if status.Version == 0 {
		status = MetadataStatus{Version: 1, Remote: s.Set.CatalogTo, Complete: v.Number == 1}
	}
	if status.Number != v.Number-1 {
		status.Complete = false
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	headCopies := int64(1)
	if s.Kinds[s.Set.CatalogTo] == "aws" {
		headCopies++
	}
	status.Bytes += info.Size() + descriptor.Size() + int64(len(b))*headCopies
	status.Objects += 2 + headCopies
	status.Revision, status.Number, status.AsOf = v.ID, v.Number, now()
	t := status.AsOf
	status.LastUpload = &t
	status.Classes = map[string]MetadataClass{"STANDARD": {Bytes: status.Bytes, Objects: status.Objects, BillableBytes: status.Bytes}}
	return s.saveJob(filepath.Join(s.Directory, "metadata-status.json"), status)
}
