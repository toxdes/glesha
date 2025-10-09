package database

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	_ "modernc.org/sqlite"
)

func TestNewDB(t *testing.T) {
	t.Run("InvalidPath", func(t *testing.T) {
		tempDir, err := os.MkdirTemp("", "test-db")
		assert.NoError(t, err)
		defer os.RemoveAll(tempDir)

		db, err := NewDB(tempDir)
		assert.NoError(t, err) // NewDB doesn't return an error for a directory

		err = db.D.Ping()
		assert.Error(t, err)
	})

	t.Run("ValidPath", func(t *testing.T) {
		db, err := NewDB(":memory:")
		assert.NoError(t, err)
		assert.NotNil(t, db)
		defer db.Close(context.Background())

		err = db.D.Ping()
		assert.NoError(t, err)
	})
}

func TestDB_createTables(t *testing.T) {
	db, err := NewDB(":memory:")
	assert.NoError(t, err)
	defer db.Close(context.Background())

	t.Run("Success", func(t *testing.T) {
		err := db.createTables(context.Background())
		assert.NoError(t, err)

		// Verify that the tables were created
		rows, err := db.D.Query("SELECT name FROM sqlite_master WHERE type='table'")
		assert.NoError(t, err)
		defer rows.Close()

		var tables []string
		for rows.Next() {
			var name string
			assert.NoError(t, rows.Scan(&name))
			tables = append(tables, name)
		}

		assert.Contains(t, tables, "tasks")
		assert.Contains(t, tables, "uploads")
		assert.Contains(t, tables, "upload_blocks")
	})
}

func TestGetDBFilePath(t *testing.T) {
	tempHome, err := os.MkdirTemp("", "test-home")
	assert.NoError(t, err)
	defer os.RemoveAll(tempHome)

	t.Setenv("HOME", tempHome)

	dbPath, err := GetDBFilePath(context.Background())
	assert.NoError(t, err)

	expectedPath := filepath.Join(tempHome, ".config", "glesha", "glesha-db.db")
	assert.Equal(t, expectedPath, dbPath)
}
