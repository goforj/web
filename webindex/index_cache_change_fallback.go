//go:build !linux && !darwin

package webindex

import "io/fs"

// indexCacheFileChangeToken disables metadata-only validation where a non-restorable change token is unavailable.
func indexCacheFileChangeToken(fs.FileInfo) (int64, int64, bool) {
	return 0, 0, false
}
