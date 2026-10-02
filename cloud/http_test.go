package cloud

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"glesha/config"
)

func TestAWSRegionSigningUsesCanonicalCase(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_SESSION_TOKEN", "")
	for _, region := range []string{"ap-southeast-1", "AP-SOUTHEAST-1", " Ap-Southeast-1 "} {
		t.Run(region, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(r.Header.Get("Authorization"), "/ap-southeast-1/s3/aws4_request") {
					t.Error("request was not signed with the lowercase region")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Length", "0")
			}))
			defer server.Close()
			provider := config.Provider{Bucket: "test", Region: region, Endpoint: server.URL}
			store, err := New(context.Background(), "aws", provider, 1, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.Head(context.Background(), "archive", ""); err != nil {
				t.Fatal(err)
			}
			if provider.Region != region {
				t.Fatal("caller configuration changed")
			}
		})
	}
}

func TestObservedLifecycleAndPinnedColdRequest(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	requested := 0
	restore := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", "123")
			w.Header().Set("ETag", `"etag"`)
			w.Header().Set("x-amz-version-id", "original-version")
			w.Header().Set("x-amz-storage-class", "DEEP_ARCHIVE")
			if restore != "" {
				w.Header().Set("x-amz-restore", restore)
			}
			return
		}
		if r.Method == http.MethodPost && r.URL.Query().Has("restore") {
			requested++
			if r.URL.Query().Get("versionId") != "original-version" {
				t.Error("cold request not version-pinned")
			}
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "<Tier>Bulk</Tier>") {
				t.Error("retrieval not Bulk")
			}
			w.WriteHeader(202)
			return
		}
		t.Errorf("unexpected request %s", r.Method)
		w.WriteHeader(500)
	}))
	defer server.Close()
	store, err := New(context.Background(), "aws", config.Provider{Bucket: "test", Region: "us-east-1", Endpoint: server.URL}, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	head, err := store.Head(context.Background(), "archive", "")
	if err != nil || head.Class != "DEEP_ARCHIVE" || !head.Cold {
		t.Fatal("lifecycle class not observed", head, err)
	}
	if _, _, err = store.Get(context.Background(), "archive", ""); err == nil {
		t.Fatal("cold GET proceeded")
	}
	if requested != 0 {
		t.Fatal("GET implicitly requested charged retrieval")
	}
	if err = store.(VersionRestorer).RestoreVersion(context.Background(), "archive", "original-version", 7, "Bulk"); err != nil {
		t.Fatal(err)
	}
	if requested != 1 {
		t.Fatal("retrieval not requested")
	}
	restore = `ongoing-request="false", expiry-date="Fri, 09 Oct 2026 00:00:00 GMT"`
	head, err = store.Head(context.Background(), "archive", "")
	if err != nil || head.Cold {
		t.Fatal("restored copy not readable", err)
	}
	if head.Class != "DEEP_ARCHIVE" {
		t.Fatal("restore changed permanent class")
	}
}
func TestIndependentAdaptersAndMissingCredentials(t *testing.T) {
	t.Setenv("B2_KEY_ID", "test")
	t.Setenv("B2_APPLICATION_KEY", "test")
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_ACCESS_KEY", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SECRET_KEY", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "0")
		w.Header().Set("ETag", `"empty"`)
	}))
	defer server.Close()
	store, err := New(context.Background(), "b2", config.Provider{Bucket: "test", Region: "test", Endpoint: server.URL}, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Head(context.Background(), "object", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = New(context.Background(), "aws", config.Provider{Bucket: "test", Region: "test"}, 1, nil); err == nil {
		t.Fatal("missing AWS credentials accepted")
	}
}
func TestListingPagination(t *testing.T) {
	t.Setenv("B2_KEY_ID", "test")
	t.Setenv("B2_APPLICATION_KEY", "test")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		key := "prefix/a"
		truncated := "true"
		if calls == 2 {
			if r.URL.Query().Get("start-after") != key {
				t.Error("wrong continuation")
			}
			key = "prefix/b"
			truncated = "false"
		}
		fmt.Fprintf(w, `<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><IsTruncated>%s</IsTruncated><Contents><Key>%s</Key><Size>3</Size><ETag>etag</ETag></Contents></ListBucketResult>`, truncated, key)
	}))
	defer server.Close()
	store, err := New(context.Background(), "b2", config.Provider{Bucket: "test", Region: "test", Endpoint: server.URL}, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	if err = List(context.Background(), store, "prefix/", func(Object) error { seen++; return nil }); err != nil {
		t.Fatal(err)
	}
	if seen != 2 || calls != 2 {
		t.Fatal("incomplete listing")
	}
}

func TestClassChangeRejectsReplacingNewerVersion(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	copies := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			version := r.URL.Query().Get("versionId")
			etag := `"old"`
			if version == "" {
				version = "new-version"
				etag = `"new"`
			}
			w.Header().Set("Content-Length", "1")
			w.Header().Set("ETag", etag)
			w.Header().Set("x-amz-version-id", version)
			return
		}
		copies++
		w.WriteHeader(500)
	}))
	defer server.Close()
	store, err := New(context.Background(), "aws", config.Provider{Bucket: "test", Region: "test", Endpoint: server.URL}, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.(ClassChanger).CopyClass(context.Background(), "archive", "old-version", "STANDARD_IA"); err == nil {
		t.Fatal("newer current object overwritten")
	}
	if copies != 0 {
		t.Fatal("copy proceeded after source conflict")
	}
}
