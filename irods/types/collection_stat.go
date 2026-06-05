package types

import (
	"fmt"
)

// IRODSCollectionStat contains irods collection statistics
type IRODSCollectionStat struct {
	TotalSize       int64 `json:"total_size"`
	DataObjectCount int64 `json:"data_object_count"`
}

// ToString stringifies the object
func (obj *IRODSCollectionStat) ToString() string {
	return fmt.Sprintf("<IRODSCollectionStat %d %d>", obj.TotalSize, obj.DataObjectCount)
}
