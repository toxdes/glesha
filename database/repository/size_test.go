package repository

import (
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkMillionFileLayout(b *testing.B) {
	for n := 0; n < b.N; n++ {
		ctx := context.Background()
		file := filepath.Join(b.TempDir(), "catalog.db")
		c, err := NewCatalogRepository(ctx, file)
		if err != nil {
			b.Fatal(err)
		}
		db := c.(*catalog).db
		queries := []string{
			`INSERT INTO nodes(id,parent,name) VALUES(1,0,'Documents')`,
			`WITH RECURSIVE n(i) AS (VALUES(2) UNION ALL SELECT i+1 FROM n WHERE i<=1000000) INSERT INTO nodes SELECT i,1,printf('document-%020d.txt',i) FROM n`,
			`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<1000001) INSERT INTO versions SELECT i,'file',1024,420,1770000000000000000,1000,1000,'',randomblob(32),'null',1 FROM n`,
			`INSERT INTO membership SELECT id,id,1,NULL FROM nodes`,
		}
		for _, query := range queries {
			if _, err = db.ExecContext(ctx, query); err != nil {
				b.Fatal(err)
			}
		}
		out := filepath.Join(b.TempDir(), "compact.db")
		if err = c.Vacuum(ctx, out); err != nil {
			b.Fatal(err)
		}
		c.Close()
		info, err := os.Stat(out)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(info.Size())/(1<<20), "MiB/catalog")
		source, err := os.Open(out)
		if err != nil {
			b.Fatal(err)
		}
		compressed, err := os.Create(filepath.Join(b.TempDir(), "catalog.db.gz"))
		if err != nil {
			b.Fatal(err)
		}
		writer := gzip.NewWriter(compressed)
		_, err = io.Copy(writer, source)
		source.Close()
		if err != nil {
			b.Fatal(err)
		}
		if err = writer.Close(); err != nil {
			b.Fatal(err)
		}
		info, err = compressed.Stat()
		compressed.Close()
		if err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(info.Size())/(1<<20), "MiB/catalog-gzip")

	}
}
