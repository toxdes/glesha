package file_io

import (
	"context"

	"github.com/stretchr/testify/mock"
)

// MockFileIO is a mock type for the FileIO type
type MockFileIO struct {
	mock.Mock
}

// IsReadable is a mock method
func (m *MockFileIO) IsReadable(path string) error {
	args := m.Called(path)
	return args.Error(0)
}

// IsWritable is a mock method
func (m *MockFileIO) IsWritable(path string) error {
	args := m.Called(path)
	return args.Error(0)
}

// ComputeFilesInfo is a mock method
func (m *MockFileIO) ComputeFilesInfo(ctx context.Context, path string, ignoredDirs map[string]bool) (*FilesInfo, error) {
	args := m.Called(ctx, path, ignoredDirs)
	return args.Get(0).(*FilesInfo), args.Error(1)
}
