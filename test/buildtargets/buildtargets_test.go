//go:build buildcheck

// Package buildtargets verifies that a cross-compiled Skopos binary matches
// the GOOS and GOARCH recorded in the CI matrix. Reading the executable header
// catches a build where the target variables were ignored and the host binary
// was uploaded under the wrong name.
package buildtargets

import (
	"debug/elf"
	"debug/pe"
	"os"
	"path/filepath"
	"testing"
)

func TestBuiltBinaryMatchesTarget(t *testing.T) {
	dir := os.Getenv("SKOPOS_BINARY_DIR")
	if dir == "" {
		t.Fatal("SKOPOS_BINARY_DIR is required")
	}
	goos := os.Getenv("SKOPOS_BINARY_GOOS")
	if goos == "" {
		t.Fatal("SKOPOS_BINARY_GOOS is required")
	}
	goarch := os.Getenv("SKOPOS_BINARY_GOARCH")
	if goarch == "" {
		t.Fatal("SKOPOS_BINARY_GOARCH is required")
	}

	entry, err := binaryIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	info, err := entry.Info()
	if err != nil {
		t.Fatalf("stat %s: %v", entry.Name(), err)
	}
	if info.Size() == 0 {
		t.Fatalf("%s is empty", entry.Name())
	}

	switch goos {
	case "linux", "freebsd":
		checkELF(t, entry.Name(), goos, goarch)
	case "windows":
		checkPE(t, entry.Name(), goarch)
	default:
		t.Fatalf("unsupported GOOS %q", goos)
	}
}

// binaryIn returns the single skopos or skopos.exe file in dir. A directory
// with both names means a previous target leaked into this run.
func binaryIn(dir string) (os.DirEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var found []os.DirEntry
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if entry.Name() == "skopos" || entry.Name() == "skopos.exe" {
			found = append(found, entry)
		}
	}
	switch len(found) {
	case 0:
		return nil, &os.PathError{Op: "find", Path: dir, Err: os.ErrNotExist}
	case 1:
		return found[0], nil
	default:
		return nil, &os.PathError{Op: "find", Path: dir, Err: os.ErrExist}
	}
}

func checkELF(t *testing.T, name, goos, goarch string) {
	t.Helper()
	file, err := elf.Open(filepath.Join(os.Getenv("SKOPOS_BINARY_DIR"), name))
	if err != nil {
		t.Fatalf("open ELF %s: %v", name, err)
	}
	defer file.Close()

	if file.Class != elf.ELFCLASS64 {
		t.Errorf("%s class = %s, want %s", name, file.Class, elf.ELFCLASS64)
	}
	wantMachine := elfMachine(t, goarch)
	if file.Machine != wantMachine {
		t.Errorf("%s machine = %s, want %s", name, file.Machine, wantMachine)
	}
	wantABI := elf.ELFOSABI_NONE
	if goos == "freebsd" {
		wantABI = elf.ELFOSABI_FREEBSD
	}
	if file.OSABI != wantABI {
		t.Errorf("%s OS ABI = %s, want %s", name, file.OSABI, wantABI)
	}
}

func elfMachine(t *testing.T, goarch string) elf.Machine {
	t.Helper()
	switch goarch {
	case "amd64":
		return elf.EM_X86_64
	case "arm64":
		return elf.EM_AARCH64
	default:
		t.Fatalf("unsupported GOARCH %q", goarch)
		return 0
	}
}

func checkPE(t *testing.T, name, goarch string) {
	t.Helper()
	file, err := pe.Open(filepath.Join(os.Getenv("SKOPOS_BINARY_DIR"), name))
	if err != nil {
		t.Fatalf("open PE %s: %v", name, err)
	}
	defer file.Close()

	var wantMachine uint16 = pe.IMAGE_FILE_MACHINE_AMD64
	switch goarch {
	case "amd64":
	case "arm64":
		wantMachine = pe.IMAGE_FILE_MACHINE_ARM64
	default:
		t.Fatalf("unsupported GOARCH %q", goarch)
	}
	if file.Machine != wantMachine {
		t.Errorf("%s machine = 0x%x, want 0x%x", name, file.Machine, wantMachine)
	}
}
