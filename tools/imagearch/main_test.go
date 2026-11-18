package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// elfHeader is a minimal 64-bit little-endian ELF header for the given machine (EM_X86_64 = 62,
// EM_AARCH64 = 183): enough for debug/elf to read the machine.
func elfHeader(machine uint16) []byte {
	h := make([]byte, 64)
	copy(h, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	binary.LittleEndian.PutUint16(h[16:], 2) // ET_EXEC
	binary.LittleEndian.PutUint16(h[18:], machine)
	binary.LittleEndian.PutUint32(h[20:], 1)  // EV_CURRENT
	binary.LittleEndian.PutUint16(h[52:], 64) // header size
	return h
}

func tarOf(t *testing.T, files map[string][]byte, gz bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	var w *tar.Writer
	var g *gzip.Writer
	if gz {
		g = gzip.NewWriter(&buf)
		w = tar.NewWriter(g)
	} else {
		w = tar.NewWriter(&buf)
	}
	for name, data := range files {
		if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if g != nil {
		if err := g.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes()
}

// imageArchive writes an image archive like `docker buildx build --output type=docker` makes: a
// manifest.json naming layer blobs, one of them (gzipped) holding the den-node binary.
func imageArchive(t *testing.T, machine uint16) string {
	t.Helper()
	certs := tarOf(t, map[string][]byte{"etc/ssl/certs/ca-certificates.crt": []byte("certs")}, true)
	binLayer := tarOf(t, map[string][]byte{"den-node": elfHeader(machine)}, true)
	manifest, _ := json.Marshal([]map[string]any{{
		"Config": "blobs/sha256/config",
		"Layers": []string{"blobs/sha256/certs", "blobs/sha256/bin"},
	}})
	archive := tarOf(t, map[string][]byte{
		"manifest.json":       manifest,
		"blobs/sha256/config": []byte("{}"),
		"blobs/sha256/certs":  certs,
		"blobs/sha256/bin":    binLayer,
	}, false)
	path := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(path, archive, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadsTheArchitectureOfTheNodeBinary(t *testing.T) {
	for want, machine := range map[string]uint16{"amd64": 62, "arm64": 183} {
		got, err := binaryArch(imageArchive(t, machine), "den-node")
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("machine %d: got %q, want %q", machine, got, want)
		}
	}
}

func TestCheckFailsWhenTheBinaryIsForAnotherArchitecture(t *testing.T) {
	// The bug this guards against: an arm64 image whose binary was compiled for x86-64.
	err := check(imageArchive(t, 62), "den-node", "arm64")
	if err == nil || !strings.Contains(err.Error(), "amd64") || !strings.Contains(err.Error(), "arm64") {
		t.Fatalf("expected a mismatch naming both architectures, got %v", err)
	}
	if err := check(imageArchive(t, 183), "den-node", "arm64"); err != nil {
		t.Fatalf("a matching binary must pass: %v", err)
	}
}

func TestCheckFailsWhenTheBinaryIsMissing(t *testing.T) {
	if err := check(imageArchive(t, 62), "other-binary", "amd64"); err == nil {
		t.Fatal("expected an error for a missing binary")
	}
}
