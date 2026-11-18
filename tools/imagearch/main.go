// Command imagearch fails when the node binary inside an image archive was compiled for another
// architecture than the image claims. deploy/bundle.sh runs it on every image it builds: an arm64
// image with an x86-64 binary looks right in every label and only fails on an ARM server.
//
//	go run ./tools/imagearch -arch arm64 dist/den-node-kit-<v>/images/den-node-<v>-arm64.tar
package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"debug/elf"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
)

// archOf maps the ELF machine of a binary to the Go architecture name images use.
var archOf = map[elf.Machine]string{
	elf.EM_X86_64:  "amd64",
	elf.EM_AARCH64: "arm64",
}

func main() {
	want := flag.String("arch", "", "the architecture the image claims (amd64 or arm64)")
	binary := flag.String("binary", "den-node", "the binary's path inside the image")
	flag.Parse()
	if *want == "" || flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: imagearch -arch amd64|arm64 [-binary den-node] <image.tar>")
		os.Exit(2)
	}
	if err := check(flag.Arg(0), *binary, *want); err != nil {
		fmt.Fprintln(os.Stderr, "imagearch:", err)
		os.Exit(1)
	}
}

func check(archive, binary, want string) error {
	got, err := binaryArch(archive, binary)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("%s: /%s is compiled for %s, but the image is %s", path.Base(archive), binary, got, want)
	}
	return nil
}

// binaryArch finds the binary in the image's layers (the last layer that has it wins, as in the
// image's file system) and reads its ELF machine.
func binaryArch(archive, binary string) (string, error) {
	files, err := readTar(archive)
	if err != nil {
		return "", err
	}
	var manifest []struct{ Layers []string }
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil || len(manifest) == 0 {
		return "", fmt.Errorf("%s: no manifest.json (is it an image archive?)", archive)
	}
	var found []byte
	for _, layer := range manifest[0].Layers {
		data, ok := files[layer]
		if !ok {
			return "", fmt.Errorf("%s: layer %s missing", archive, layer)
		}
		content, err := fileInLayer(data, binary)
		if err != nil {
			return "", fmt.Errorf("%s: layer %s: %w", archive, layer, err)
		}
		if content != nil {
			found = content
		}
	}
	if found == nil {
		return "", fmt.Errorf("%s: no /%s in any layer", archive, binary)
	}
	f, err := elf.NewFile(bytes.NewReader(found))
	if err != nil {
		return "", fmt.Errorf("/%s is not an ELF binary: %w", binary, err)
	}
	arch, ok := archOf[f.Machine]
	if !ok {
		return "", fmt.Errorf("/%s is for an unexpected machine %s", binary, f.Machine)
	}
	return arch, nil
}

func readTar(name string) (map[string][]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return entries(f)
}

func entries(r io.Reader) (map[string][]byte, error) {
	out := map[string][]byte{}
	t := tar.NewReader(r)
	for {
		h, err := t.Next()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		data, err := io.ReadAll(t)
		if err != nil {
			return nil, err
		}
		out[path.Clean(h.Name)] = data
	}
}

// fileInLayer returns the file's content if the layer (gzipped or plain tar) has it, else nil.
func fileInLayer(layer []byte, name string) ([]byte, error) {
	var r io.Reader = bytes.NewReader(layer)
	br := bufio.NewReader(r)
	if magic, _ := br.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		r = gz
	} else {
		r = br
	}
	files, err := entries(r)
	if err != nil {
		return nil, err
	}
	return files[path.Clean(name)], nil
}
