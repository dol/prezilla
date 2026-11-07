//go:build !unix

package core

// Lock is a no-op on non-unix platforms.
// TODO: the Windows backend plan must provide a real cross-process lock
// (e.g. LockFileEx on <state path>.lock) before Windows is supported.
func (s *Store) Lock() (unlock func(), err error) {
	return func() {}, nil
}
