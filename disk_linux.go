package main

import "syscall"

// diskUsage returns total and used bytes of the filesystem holding path.
func diskUsage(path string) (total, used int64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		if err := syscall.Statfs("/", &st); err != nil {
			return 0, 0
		}
	}
	total = int64(st.Blocks) * int64(st.Bsize)
	free := int64(st.Bavail) * int64(st.Bsize)
	return total, total - free
}
