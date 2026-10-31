package app_cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"glesha/backup"
	"glesha/config"
	"glesha/database/model"
	L "glesha/logger"
)

func TestCatalogSyncGroupsMissingBucketFailures(t *testing.T) {
	r := portableRuntime(t, "")
	r.config.AWS = config.Provider{}
	var ids []string
	for _, remote := range []model.Remote{
		{Name: "aws", Kind: "aws", Bucket: "missing-aws"},
		{Name: "b2", Kind: "b2", Bucket: "missing-b2"},
	} {
		bound, err := r.registry.BindRemote(r.ctx, remote)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, bound.ID)
	}
	var sets []model.Set
	for i := 0; i < 12; i++ {
		set, err := backup.NewSet(fmt.Sprintf("set-%d", i), nil, r.config)
		if err != nil {
			t.Fatal(err)
		}
		set.CatalogTo = ids[i%2]
		if err := r.registry.Create(r.ctx, set); err != nil {
			t.Fatal(err)
		}
		sets = append(sets, set)
	}
	if err := L.SetColorModeFromString("yes"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { L.SetColorModeFromString("auto") })
	err := catalogSync(r, nil)
	if err == nil || ExitCode(err) == 0 {
		t.Fatal("incomplete sync reported success")
	}
	if strings.Contains(err.Error(), "missing-") || errors.Unwrap(err) == nil {
		t.Fatalf("returned error should be concise and preserve causes: %v", err)
	}
	output := r.out.(*bytes.Buffer).String()
	for _, bucket := range []string{"missing-aws", "missing-b2"} {
		if strings.Count(output, bucket) != 1 {
			t.Fatalf("bucket error repeated: %s", output)
		}
	}
	if strings.Count(output, "6 catalogs:") != 2 || strings.Count(strings.TrimSpace(output), "\n") != 2 {
		t.Fatalf("expected two groups and one summary: %s", output)
	}
	if strings.Count(output, "\x1b[31m") != 3 || strings.Count(output, "\x1b[36m") != 0 {
		t.Fatalf("failures were not red: %q", output)
	}
	for _, set := range sets {
		if strings.Contains(output, set.ID) {
			t.Fatalf("grouped output exposed individual UUID: %s", output)
		}
	}
	r.out.(*bytes.Buffer).Reset()
	r.env.Format = "json"
	if err := catalogSync(r, nil); err == nil {
		t.Fatal("JSON incomplete sync reported success")
	}
	if strings.Contains(r.out.(*bytes.Buffer).String(), "\x1b") {
		t.Fatal("JSON contains ANSI escapes")
	}
	decoder := json.NewDecoder(r.out.(*bytes.Buffer))
	failed, summaries := 0, 0
	for {
		var record struct {
			Kind string
			Data json.RawMessage
		}
		if err := decoder.Decode(&record); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		switch record.Kind {
		case "catalog_state":
			var result catalogSyncResult
			if err := json.Unmarshal(record.Data, &result); err != nil {
				t.Fatal(err)
			}
			if result.State != backup.CatalogStateFailed || result.Error == "" {
				t.Fatalf("lost per-set failure: %+v", result)
			}
			failed++
		case "catalog_sync_summary":
			var summary catalogSyncSummary
			if err := json.Unmarshal(record.Data, &summary); err != nil {
				t.Fatal(err)
			}
			if summary.Failed != 12 {
				t.Fatalf("incorrect failure count: %+v", summary)
			}
			summaries++
		default:
			t.Fatalf("unexpected record: %s", record.Kind)
		}
	}
	if failed != 12 || summaries != 1 {
		t.Fatalf("lost JSON records: %d failures, %d summaries", failed, summaries)
	}
	if err := L.SetColorModeFromString("no"); err != nil {
		t.Fatal(err)
	}
	r.env.Format = "plain"
	r.out.(*bytes.Buffer).Reset()
	if err := catalogSync(r, nil); err == nil {
		t.Fatal("color-disabled incomplete sync reported success")
	}
	if strings.Contains(r.out.(*bytes.Buffer).String(), "\x1b") {
		t.Fatal("color-disabled report contains ANSI escapes")
	}
}

func TestCatalogSyncReportsOperationErrorsInJSON(t *testing.T) {
	r := portableRuntime(t, "")
	r.env.Format = "json"
	cause := errors.New("bucket discovery unavailable")
	failures := catalogFailures{}
	if err := failures.addOperation(r, "Discovery", cause); err != nil {
		t.Fatal(err)
	}
	if err := failures.finish(r, catalogSyncSummary{Failed: 1}); !errors.Is(err, cause) {
		t.Fatalf("lost discovery error: %v", err)
	}
	var record struct {
		Kind string
		Data struct {
			Operation string
			Error     string
		}
	}
	if err := json.NewDecoder(r.out.(*bytes.Buffer)).Decode(&record); err != nil {
		t.Fatal(err)
	}
	if record.Kind != "catalog_sync_error" || record.Data.Operation != "Discovery" || record.Data.Error != cause.Error() {
		t.Fatalf("missing discovery error details: %+v", record)
	}
}

func TestCatalogSyncErrorPreservesExitCodes(t *testing.T) {
	for _, cause := range []error{backup.ErrConflict, backup.ErrPending, errors.New("unavailable"), errors.New("cli: invalid selection")} {
		t.Run(cause.Error(), func(t *testing.T) {
			err := &reportedCommandError{command: "catalog sync", cause: cause}
			if !errors.Is(err, cause) || ExitCode(err) != ExitCode(cause) {
				t.Fatalf("error identity or exit code changed: %v", err)
			}
		})
	}
}
