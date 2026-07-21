//go:build linux

package webindex

import (
	"io/fs"
	"syscall"
)

// indexCacheFileChangeToken returns Linux ctime, which cannot be restored through ordinary file timestamp APIs.
func indexCacheFileChangeToken(info fs.FileInfo) (int64, int64, bool) {
	state, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return state.Ctim.Sec, state.Ctim.Nsec, true
}
