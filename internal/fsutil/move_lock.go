package fsutil

type Unlocker interface {
	Unlock() error
}
