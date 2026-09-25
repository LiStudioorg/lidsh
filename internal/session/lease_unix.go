//go:build unix

package session

import (
	"os"
	"syscall"
)

// flockExclusive 非阻塞独占锁（LOCK_EX|LOCK_NB）。
func flockExclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func funlock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
