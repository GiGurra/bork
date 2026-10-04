package driver

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestDarwinCompilerRegionDecoder(t *testing.T) {
	data := make([]byte, darwinImageRegionBytes)
	order := binary.LittleEndian
	order.PutUint32(data[:4], 4)
	order.PutUint64(data[80:88], 0x1000)
	order.PutUint64(data[88:96], 0x1000)
	order.PutUint32(data[96:100], 17)
	order.PutUint16(data[100:102], 0100000|0755)
	order.PutUint64(data[104:112], 99)
	order.PutUint64(data[184:192], 12345)
	order.PutUint32(data[232:236], 1)
	image, ok := decodeDarwinMappedImage(data, 0x1001)
	if !ok || image.device != 17 || image.inode != 99 || image.size != 12345 {
		t.Fatalf("invalid ABI decode: %+v %v", image, ok)
	}
	for _, address := range []uint64{0xfff, 0x2000, ^uint64(0)} {
		if _, ok := decodeDarwinMappedImage(data, address); ok {
			t.Fatal("address outside returned mapping accepted")
		}
	}
	for _, offset := range []int{0, 100, 104, 184, 232} {
		changed := append([]byte(nil), data...)
		clear(changed[offset : offset+8])
		if _, ok := decodeDarwinMappedImage(changed, 0x1001); ok {
			t.Fatalf("invalid mapping field at%d accepted", offset)
		}
	}
	if _, ok := decodeDarwinMappedImage(data[:len(data)-1], 0x1001); ok {
		t.Fatal("short ABI accepted")
	}
}

func TestDarwinCompilerPathReplacementDeclines(t *testing.T) {
	if os.Getenv("BORK_DARWIN_COMPILER_HELPER") == "1" {
		original, err := openCachePublisherImage()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = original.Close() }()
		if _, err := hashCompilerImage(); err != nil {
			t.Fatal(err)
		}
		ready := os.NewFile(3, "identity-ready")
		if _, err := ready.Write([]byte{1}); err != nil {
			t.Fatal(err)
		}
		_ = ready.Close()
		var release [1]byte
		if _, err := io.ReadFull(os.Stdin, release[:]); err != nil {
			t.Fatal(err)
		}
		info, err := original.Stat()
		if err != nil || !sameRunningImage(info) {
			t.Fatal("mapped vnode changed after pathname replacement", err)
		}
		if _, err := hashCompilerImage(); !errors.Is(err, errCompilerImageUnavailable) {
			t.Fatalf("replacement compiler bytes accepted: %v", err)
		}
		return
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(t.TempDir(), "compiler")
	if err := os.WriteFile(program, data, 0700); err != nil {
		t.Fatal(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close(); _ = writer.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, program, "-test.run=^TestDarwinCompilerPathReplacementDeclines$")
	command.Env = append(os.Environ(), "BORK_DARWIN_COMPILER_HELPER=1")
	command.ExtraFiles = []*os.File{writer}
	command.WaitDelay = time.Second
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output := &identityOutput{}
	command.Stdout, command.Stderr = output, output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill() }()
	_ = writer.Close()
	var ready [1]byte
	if _, err := io.ReadFull(reader, ready[:]); err != nil {
		t.Fatalf("helper readiness: %v %s", err, output.String())
	}
	if err := os.Rename(program, program+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(program, []byte("replacement"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := input.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	_ = input.Close()
	if err := command.Wait(); err != nil {
		t.Fatalf("helper: %v %s", err, output.String())
	}
}

func TestDarwinCompilerSDKLayout(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("native SDK compiler unavailable; kernel identity tests still apply")
	}
	root := t.TempDir()
	path := filepath.Join(root, "layout.c")
	code := `#include <stddef.h>
#include <sys/proc_info.h>
_Static_assert(sizeof(struct proc_regioninfo) == 96, "region metadata size");
_Static_assert(sizeof(struct proc_regionwithpathinfo) == 1272, "region with vnode size");
_Static_assert(offsetof(struct proc_regionwithpathinfo, prp_prinfo.pri_address) == 80, "region address");
_Static_assert(offsetof(struct proc_regionwithpathinfo, prp_vip.vip_vi.vi_stat.vst_dev) == 96, "vnode device");
_Static_assert(offsetof(struct proc_regionwithpathinfo, prp_vip.vip_vi.vi_stat.vst_ino) == 104, "vnode inode");
_Static_assert(offsetof(struct proc_regionwithpathinfo, prp_vip.vip_vi.vi_stat.vst_size) == 184, "vnode size");
_Static_assert(offsetof(struct proc_regionwithpathinfo, prp_vip.vip_vi.vi_type) == 232, "vnode type");
`
	if err := os.WriteFile(path, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, cc, "-std=c11", "-c", path, "-o", filepath.Join(root, "layout.o"))
	command.WaitDelay = time.Second
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("unsupported SDK ABI: %v %s", err, output)
	}
}
