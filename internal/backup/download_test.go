package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"glesha/internal/cloud"
)

func TestUploadStorageClassNeedsCapableDestination(t *testing.T) {
	ctx := context.Background()
	s, store, _ := setupService(t)
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Upload(ctx, s.Set.To, "f", file, "GLACIER", false); err == nil || !strings.Contains(err.Error(), "storage class requires") {
		t.Fatalf("destination without storage classes accepted GLACIER: %v", err)
	}
	if err := s.Upload(ctx, s.Set.To, "f", file, "PLATINUM", false); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("accepted an unknown storage class: %v", err)
	}
	if store.puts["f"] != 0 {
		t.Fatal("rejected upload reached the store")
	}
	useProvider(t, s, s.Set.To[0], cloud.AWS)
	if err := s.Upload(ctx, s.Set.To, "f", file, "GLACIER", false); err != nil {
		t.Fatal(err)
	}
}
