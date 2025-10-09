package archive

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"glesha/database"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewTarGzArchiver(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-archiver")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Test case 1: Input path is not readable
	t.Run("InputPathNotReadable", func(t *testing.T) {
		inputPath := filepath.Join(tempDir, "non-existent-input")
		outputPath := filepath.Join(tempDir, "output")
		task := &database.GleshaTask{
			ID:        1,
			InputPath: inputPath,
			OutputPath: outputPath,
		}

		_, err := NewTarGzArchiver(task)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), fmt.Sprintf("no read permission on input path: %s", inputPath))
	})

	// Test case 2: Output path is not writable
	t.Run("OutputPathNotWritable", func(t *testing.T) {
		inputPath := filepath.Join(tempDir, "input")
		err := os.Mkdir(inputPath, 0755)
		assert.NoError(t, err)

		outputPath := filepath.Join(tempDir, "non-writable-output")
		err = os.Mkdir(outputPath, 0400) // Read-only
		assert.NoError(t, err)

		task := &database.GleshaTask{
			ID:        1,
			InputPath: inputPath,
			OutputPath: outputPath,
		}

		_, err = NewTarGzArchiver(task)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), fmt.Sprintf("no write permission on output path: %s", outputPath))
	})

	// Test case 3: Valid input and output paths
	t.Run("ValidPaths", func(t *testing.T) {
		inputPath := filepath.Join(tempDir, "input-valid")
		err := os.Mkdir(inputPath, 0755)
		assert.NoError(t, err)

		outputPath := filepath.Join(tempDir, "output-valid")
		err = os.Mkdir(outputPath, 0755)
		assert.NoError(t, err)

		task := &database.GleshaTask{
			ID:        1,
			InputPath: inputPath,
			OutputPath: outputPath,
		}

		archiver, err := NewTarGzArchiver(task)
		assert.NoError(t, err)
		assert.NotNil(t, archiver)
		assert.Equal(t, task.ID, archiver.ID)
		assert.Equal(t, task.InputPath, archiver.InputPath)
		assert.Equal(t, task.OutputPath, archiver.OutputPath)
	})
}

func TestTarGzArchive_Plan(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-plan")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	inputPath := filepath.Join(tempDir, "input")
	err = os.Mkdir(inputPath, 0755)
	assert.NoError(t, err)

	// Create a dummy file
	file, err := os.Create(filepath.Join(inputPath, "dummy.txt"))
	assert.NoError(t, err)
	file.WriteString("hello world")
	file.Close()

	outputPath := filepath.Join(tempDir, "output")
	err = os.Mkdir(outputPath, 0755)
	assert.NoError(t, err)

	task := &database.GleshaTask{
		ID:        1,
		InputPath: inputPath,
		OutputPath: outputPath,
	}

	archiver, err := NewTarGzArchiver(task)
	assert.NoError(t, err)

	// Test case 1: Successful plan
	t.Run("SuccessfulPlan", func(t *testing.T) {
		err := archiver.Plan(context.Background())
		assert.NoError(t, err)
		assert.NotNil(t, archiver.Info)
		assert.Equal(t, uint64(1), archiver.Info.TotalFileCount)
		assert.Equal(t, uint64(11), archiver.Info.SizeInBytes)
		assert.Equal(t, STATUS_PLANNED, archiver.Progress.Status)
	})
}

func createDummyFile(t *testing.T, path, content string) {
	file, err := os.Create(path)
	assert.NoError(t, err)
	_, err = file.WriteString(content)
	assert.NoError(t, err)
	file.Close()
}

func TestTarGzArchive_Archive(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-archive")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	inputPath := filepath.Join(tempDir, "input")
	err = os.Mkdir(inputPath, 0755)
	assert.NoError(t, err)

	outputPath := filepath.Join(tempDir, "output")
	err = os.Mkdir(outputPath, 0755)
	assert.NoError(t, err)

	task := &database.GleshaTask{
		ID:        1,
		InputPath: inputPath,
		OutputPath: outputPath,
	}

	archiver, err := NewTarGzArchiver(task)
	assert.NoError(t, err)

	// Test case 1: Successful archive creation
	t.Run("SuccessfulArchive", func(t *testing.T) {
		createDummyFile(t, filepath.Join(inputPath, "file1.txt"), "file1 content")
		createDummyFile(t, filepath.Join(inputPath, "file2.txt"), "file2 content")

		err = archiver.Plan(context.Background())
		assert.NoError(t, err)

		err = archiver.archive(context.Background())
		assert.NoError(t, err)

		// Verify the archive file exists and is valid
		archivePath := archiver.getTarFile()
		assert.FileExists(t, archivePath)
		err = IsValidTarGz(archivePath)
		assert.NoError(t, err)
	})

	// Test case 2: Context canceled during archive
	t.Run("ContextCanceled", func(t *testing.T) {
		createDummyFile(t, filepath.Join(inputPath, "file3.txt"), "file3 content")

		err = archiver.Plan(context.Background())
		assert.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err = archiver.archive(ctx)
		assert.NoError(t, err) // The error is not propagated, but the archive is not created

		// Verify the archive file does not exist
		archivePath := archiver.getTarFile()
		assert.NoFileExists(t, archivePath)
	})
}

func TestIsValidTarGz(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-is-valid-targz")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Test case 1: Valid tar.gz file
	t.Run("ValidTarGz", func(t *testing.T) {
		// Create a dummy tar.gz file
		archivePath := filepath.Join(tempDir, "valid.tar.gz")
		file, err := os.Create(archivePath)
		assert.NoError(t, err)

		gzipWriter := gzip.NewWriter(file)
		tarWriter := tar.NewWriter(gzipWriter)
		tarWriter.Close()
		gzipWriter.Close()
		file.Close()

		err = IsValidTarGz(archivePath)
		assert.NoError(t, err)
	})

	// Test case 2: Invalid tar.gz file
	t.Run("InvalidTarGz", func(t *testing.T) {
		invalidFile := filepath.Join(tempDir, "invalid.txt")
		createDummyFile(t, invalidFile, "this is not a tar.gz file")

		err = IsValidTarGz(invalidFile)
		assert.Error(t, err)
	})

	// Test case 3: File does not exist
	t.Run("FileDoesNotExist", func(t *testing.T) {
		nonExistentFile := filepath.Join(tempDir, "non-existent.tar.gz")
		err = IsValidTarGz(nonExistentFile)
		assert.Error(t, err)
	})
}

