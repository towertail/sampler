//go:build !darwin

package collect

import "github.com/shirou/gopsutil/v4/disk"

func partitions() ([]disk.PartitionStat, error) {
	return disk.Partitions(false)
}
