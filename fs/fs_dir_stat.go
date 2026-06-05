package fs

import (
	"fmt"

	"github.com/cyverse/go-irodsclient/irods/types"
)

// DirStat is a struct for directory statistics
type DirStat struct {
	Path      string `json:"path"`
	TotalSize int64  `json:"total_size"`
	FileCount int64  `json:"file_count"`
	Recursive bool   `json:"recursive"`
}

func NewDirStat(path string, recursive bool, stat *types.IRODSCollectionStat) *DirStat {
	return &DirStat{
		Path:      path,
		TotalSize: stat.TotalSize,
		FileCount: stat.DataObjectCount,
		Recursive: recursive,
	}
}

// ToString stringifies the object
func (dstat *DirStat) ToString() string {
	return fmt.Sprintf("<DirStat %s %d %d %t>", dstat.Path, dstat.TotalSize, dstat.FileCount, dstat.Recursive)
}
