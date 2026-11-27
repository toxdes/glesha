// Command e2ecleanup deletes every stored version of one backup set's remote
// data, so a run of tests/e2e.py leaves nothing billed behind. Discovery
// records are replicated to every reachable remote, and any copy left behind
// would bring the set back, so those are deleted from every configured remote.
//
//	go run ./tests/e2ecleanup --env FILE --remote REMOTE --set SET_UUID [--dry-run]
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/google/uuid"

	"glesha/internal/cloud"
	"glesha/internal/cloud/s3store"
	"glesha/internal/config"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "e2ecleanup:", err)
		os.Exit(1)
	}
}

func run() error {
	env := flag.String("env", "", "credentials file, as for glesha --env")
	configPath := flag.String("config", "", "glesha configuration file (default: glesha's default)")
	remote := flag.String("remote", "", "remote holding the set")
	set := flag.String("set", "", "UUID of the set to delete")
	dryRun := flag.Bool("dry-run", false, "list the versions without deleting them")
	flag.Parse()

	// a parsed UUID keeps every prefix specific to the one set
	id, err := uuid.Parse(*set)
	if err != nil || id.String() != *set {
		return fmt.Errorf("--set must be a set UUID")
	}
	c, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if err = config.Env(*env); err != nil {
		return err
	}
	c.ProviderEnv()
	remotes := c.RemotesByName()
	if _, ok := remotes[*remote]; !ok {
		return fmt.Errorf("unknown remote %q", *remote)
	}

	catalog := strings.TrimSuffix(c.Catalog.Prefix, "/")
	// NOTE: these follow the key layout in internal/backup; tests/e2e.py fails
	// when the archives or sets prefix is empty, which catches a layout change
	ctx := context.Background()
	for _, name := range slices.Sorted(maps.Keys(remotes)) {
		p := remotes[name]
		if p.Bucket == "" {
			continue
		}
		prefixes := []string{catalog + "/directory/v1/" + *set + "/"}
		if name == *remote {
			prefixes = append([]string{"glesha/archives/" + *set + "/", catalog + "/sets/" + *set + "/"}, prefixes...)
		}
		store, err := s3store.New(ctx, cloud.Remote{Kind: p.Kind, Bucket: p.Bucket, Region: p.Region, Endpoint: p.Endpoint}, 1, nil)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		for _, prefix := range prefixes {
			n, err := deleteVersions(ctx, store, prefix, *dryRun)
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			fmt.Printf("%d %s %s\n", n, name, prefix)
		}
	}
	return nil
}

// deleteVersions deletes every version and delete marker under prefix and
// returns how many there were.
func deleteVersions(ctx context.Context, store cloud.Store, prefix string, dryRun bool) (int, error) {
	versions, ok := store.(interface {
		cloud.VersionLister
		cloud.VersionDeleter
	})
	if !ok {
		return 0, errors.New("remote cannot list and delete object versions")
	}
	var found []cloud.Object
	if err := versions.ListVersions(ctx, prefix, func(o cloud.Object) error {
		found = append(found, o)
		return nil
	}); err != nil {
		return 0, fmt.Errorf("list %s: %w", prefix, err)
	}
	for _, o := range found {
		if dryRun {
			fmt.Printf("would delete %s (%s)\n", o.Key, o.Version)
			continue
		}
		if err := versions.DeleteVersion(ctx, o.Key, o.Version); err != nil {
			return 0, fmt.Errorf("delete %s (%s): %w", o.Key, o.Version, err)
		}
	}
	return len(found), nil
}
