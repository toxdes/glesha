package file_io

import (
	"os"
	"runtime"
)

func SamePortableMetadata(mode, mtime, expectedMode, expectedMtime int64) bool {
	if runtime.GOOS == "windows" {
		return mode&0200 == expectedMode&0200 && mtime/100 == expectedMtime/100
	}
	return mode == expectedMode && mtime == expectedMtime
}

func Permissions(mode int64) os.FileMode {
	p := os.FileMode(mode) & 0777
	if mode&04000 != 0 {
		p |= os.ModeSetuid
	}
	if mode&02000 != 0 {
		p |= os.ModeSetgid
	}
	if mode&01000 != 0 {
		p |= os.ModeSticky
	}
	return p
}
func SetOwner(root *os.Root, name string, uid, gid int, link bool) error {
	if os.Geteuid() != 0 {
		return nil
	}
	if link {
		return root.Lchown(name, uid, gid)
	}
	return root.Chown(name, uid, gid)
}
