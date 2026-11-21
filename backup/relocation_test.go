package backup

import (
	"context"
	"errors"
	"testing"

	"glesha/database/model"
)

func TestFollowAuthorityStopsRedirectLoops(t *testing.T) {
	s := &Service{}
	if err := s.followAuthority(context.Background(), revision{}, maximumAuthorityRedirects); err != nil {
		t.Fatalf("a revision without a redirect is not a hop: %v", err)
	}
	err := s.followAuthority(context.Background(), revision{Redirect: &model.Locator{}}, maximumAuthorityRedirects)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("redirect past the limit = %v, want ErrConflict", err)
	}
}
