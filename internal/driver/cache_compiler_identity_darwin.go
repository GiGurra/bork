package driver

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"reflect"
	"runtime"
	"syscall"
	"unsafe"
)

// Darwin's public proc_regionwithpathinfo ABI is 96 bytes of region metadata,
// 152 bytes of vnode metadata, then a 1024-byte path on amd64 and arm64.
// https://github.com/apple-oss-distributions/xnu/blob/main/bsd/sys/proc_info.h
const darwinImageRegionBytes = 1272

type darwinMappedImage struct {
	device uint32
	inode  uint64
	size   int64
}

func decodeDarwinMappedImage(data []byte, address uint64) (darwinMappedImage, bool) {
	if len(data) != darwinImageRegionBytes {
		return darwinMappedImage{}, false
	}
	order := binary.LittleEndian
	start, size := order.Uint64(data[80:88]), order.Uint64(data[88:96])
	if address < start || address-start >= size || order.Uint32(data[:4])&4 == 0 || order.Uint16(data[100:102])&syscall.S_IFMT != syscall.S_IFREG || order.Uint32(data[232:236]) != 1 {
		return darwinMappedImage{}, false
	}
	image := darwinMappedImage{device: order.Uint32(data[96:100]), inode: order.Uint64(data[104:112]), size: int64(order.Uint64(data[184:192]))}
	return image, image.inode != 0 && image.size > 0
}

func mappedCompilerImage() (darwinMappedImage, bool) {
	address := reflect.ValueOf(hashCompilerImage).Pointer()
	var data [darwinImageRegionBytes]byte
	// proc_pidinfo(pid, PROC_PIDREGIONPATHINFO, codeAddress, data, sizeof(data)).
	// PROC_INFO_CALL_PIDINFO=2; PROC_PIDREGIONPATHINFO=8.
	n, _, err := syscall.Syscall6(syscall.SYS_PROC_INFO, 2, uintptr(os.Getpid()), 8, address, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)))
	runtime.KeepAlive(&data)
	if err != 0 || n != uintptr(len(data)) {
		return darwinMappedImage{}, false
	}
	return decodeDarwinMappedImage(data[:], uint64(address))
}

func sameRunningImage(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() {
		return false
	}
	image, ok := mappedCompilerImage()
	return ok && image.device == uint32(stat.Dev) && image.inode == stat.Ino && image.size == info.Size()
}

func openCachePublisherImage() (*os.File, error) {
	path, err := os.Executable()
	if err != nil {
		return nil, errCompilerImageUnavailable
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !sameRunningImage(info) {
		_ = file.Close()
		return nil, errCompilerImageUnavailable
	}
	return file, nil
}
func cachePublisherExecutable(image *os.File) string { return image.Name() }

func hashCompilerImage() ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	file, err := openCachePublisherImage()
	if err != nil {
		return digest, fmt.Errorf("%w: %v", errCompilerImageUnavailable, err)
	}
	defer func() { _ = file.Close() }()
	before, err := file.Stat()
	if err != nil {
		return digest, errCompilerImageUnavailable
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return digest, err
	}
	after, err := file.Stat()
	if err != nil || !sameRunningImage(after) || !os.SameFile(before, after) || before.Size() != after.Size() || before.ModTime() != after.ModTime() || before.Mode() != after.Mode() {
		return digest, errCompilerImageUnavailable
	}
	first, last := before.Sys().(*syscall.Stat_t), after.Sys().(*syscall.Stat_t)
	if first.Ctimespec != last.Ctimespec {
		return digest, errCompilerImageUnavailable
	}
	// Installed compiler images are immutable while running; upgrades replace
	// pathnames atomically. Kernel vnode evidence rejects a replaced pathname.
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}
