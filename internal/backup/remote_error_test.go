package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"glesha/internal/database/model"
)

func TestUploadFailureNamesTheFailedRemote(t *testing.T) {
	ctx := context.Background()
	s, _, source := setupService(t)
	if err := os.WriteFile(filepath.Join(source, "file"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	offsite, err := s.Registry.BindRemote(ctx, model.Remote{Name: "offsite", Kind: "b2", Bucket: "offsite"})
	if err != nil {
		t.Fatal(err)
	}
	failing := newStore()
	failing.fail = "glesha/archives/"
	s.Stores[offsite.ID] = failing
	s.Set.To = append(s.Set.To, offsite.ID)
	if err = s.Registry.Save(ctx, s.Set); err != nil {
		t.Fatal(err)
	}

	_, err = s.Run(ctx, RunOptions{Password: []byte("password")})
	if !errors.Is(err, ErrPending) {
		t.Fatalf("failed upload: got %v, want ErrPending", err)
	}
	if message := err.Error(); !strings.Contains(message, `remote "offsite"`) || strings.Contains(message, `remote "b2"`) {
		t.Fatalf("error does not name only the failed remote: %v", err)
	}
}
