package repository

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"glesha/database/model"
)

func TestPrivateNameCollisionsRemainAddressableByUUID(t *testing.T) {
	ctx := context.Background()
	r, err := NewRegistryRepository(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	first := model.Set{ID: uuid.NewString(), Name: "music"}
	second := model.Set{ID: uuid.NewString()}
	second.Name = second.ID
	for _, set := range []model.Set{first, second} {
		if err = r.Create(ctx, set); err != nil {
			t.Fatal(err)
		}
	}
	second.Name = "music"
	if err = r.Save(ctx, second); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Set(ctx, "music"); !errors.Is(err, ErrAmbiguousSetName) {
		t.Fatal("ambiguous name silently selected a set", err)
	}
	for _, set := range []model.Set{first, second} {
		got, err := r.Set(ctx, set.ID)
		if err != nil || got.ID != set.ID || got.Name != set.Name {
			t.Fatal("private name was lost", err)
		}
	}
}

func TestUnknownStateProtocolLeavesCatalogUnchanged(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "catalog.db")
	c, err := NewCatalogRepository(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.SetMeta(ctx, model.StateProtocolKey, "99"); err != nil {
		t.Fatal(err)
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if c, err = NewCatalogRepository(ctx, file); err == nil {
		c.Close()
		t.Fatal("unknown state protocol accepted")
	}
	after, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("unsupported state was modified", err)
	}
}
