package backup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"glesha/cloud"
	"glesha/database/model"
	"glesha/pricing"
)

func TestMetadataPublicationSizesAndRetry(t *testing.T) {
	for _, kind := range []string{"aws", "b2"} {
		t.Run(kind, func(t *testing.T) {
			s := &Service{Directory: t.TempDir(), Set: model.Set{CatalogTo: "remote"}, Kinds: map[string]string{"remote": kind}}
			v := revision{Version: 2, ID: "first", Number: 1}
			file := filepath.Join(s.Directory, "publication-first")
			if err := os.WriteFile(file, []byte("batch"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file+".descriptor", []byte("descriptor"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := s.recordMetadataPublication(v); err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(v)
			copies := int64(1)
			if kind == "aws" {
				copies++
			}
			status, err := s.MetadataStatus()
			if err != nil || !status.Complete || status.Bytes != int64(len("batchdescriptor"))+int64(len(b))*copies || status.Objects != 2+copies || status.LastUpload == nil {
				t.Fatal(status, err)
			}
			if err = s.recordMetadataPublication(v); err != nil {
				t.Fatal(err)
			}
			repeated, _ := s.MetadataStatus()
			if repeated.Bytes != status.Bytes {
				t.Fatal("retry counted publication twice")
			}
		})
	}
}

func TestMetadataStatusMatchesPublishedObjects(t *testing.T) {
	for _, kind := range []string{"aws", "b2"} {
		t.Run(kind, func(t *testing.T) {
			s, store, source := setupService(t)
			s.Kinds[s.Set.CatalogTo] = kind
			if err := os.WriteFile(filepath.Join(source, "A.txt"), []byte("content"), 0600); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"first", "second"} {
				v, err := s.Run(context.Background(), RunOptions{Password: []byte("test password"), Compression: "gzip", Output: filepath.Join(t.TempDir(), name+".tar.gz.gpg")})
				if err != nil {
					t.Fatal(err)
				}
				c, err := s.Catalog(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				locations, err := c.Locations(context.Background(), v.ID)
				c.Close()
				if err != nil || len(locations) != 1 || locations[0].UploadedAt == nil {
					t.Fatal("upload time missing", locations, err)
				}
				var bytes, objects int64
				for key, object := range store.objects {
					if strings.HasPrefix(key, s.prefix()) {
						bytes += object.Size
						objects++
					}
				}
				status, err := s.MetadataStatus()
				if err != nil || !status.Complete || status.Bytes != bytes || status.Objects != objects {
					t.Fatal(status, bytes, objects, err)
				}
			}
		})
	}
}

func TestMetadataRefreshAccountsForClassesAndDates(t *testing.T) {
	store := newStore()
	s := &Service{Directory: t.TempDir(), Set: model.Set{ID: "set", CatalogTo: "remote"}, Stores: map[string]cloud.Store{"remote": store}}
	s.Config.Catalog.Prefix = "glesha/v2"
	date := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	store.objects[s.prefix()+"first"] = cloud.Object{Size: 1, Class: "STANDARD_IA", ModifiedAt: &date}
	store.objects[s.prefix()+"second"] = cloud.Object{Size: 2, Class: "STANDARD_IA"}
	store.objects["other-set"] = cloud.Object{Size: 1000}
	if err := s.RefreshMetadataStatus(context.Background()); err != nil {
		t.Fatal(err)
	}
	status, err := s.MetadataStatus()
	if err != nil || !status.Complete || status.Bytes != 3 || status.Objects != 2 || status.Classes["STANDARD_IA"].BillableBytes != 2*pricing.MinimumIABytes || !status.LastUpload.Equal(date) {
		t.Fatal(status, err)
	}
}

func TestUnknownMetadataCacheVersionIsPreserved(t *testing.T) {
	s := &Service{Directory: t.TempDir()}
	file := filepath.Join(s.Directory, "metadata-status.json")
	b := []byte(`{"version":99,"bytes":0,"objects":0}`)
	if err := os.WriteFile(file, b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MetadataStatus(); err == nil {
		t.Fatal("future cache accepted")
	}
	after, _ := os.ReadFile(file)
	if string(after) != string(b) {
		t.Fatal("future cache modified")
	}
}
