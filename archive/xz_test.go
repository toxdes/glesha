package archive

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ulikunitz/xz"

	"glesha/memory"
)

func xzData(t *testing.T, data []byte, dictionary int, blockSize int64) []byte {
	t.Helper()
	var out bytes.Buffer
	w, err := (xz.WriterConfig{DictCap: dictionary, BlockSize: blockSize}).NewWriter(&out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestXZStreamsAndIntegrity(t *testing.T) {
	random := make([]byte, 128<<10)
	rand.New(rand.NewSource(1)).Read(random)
	for _, test := range []struct {
		name  string
		data  []byte
		block int64
	}{
		{"compressed", bytes.Repeat([]byte("archive content"), 2000), 0},
		{"uncompressed chunks", random, 0},
		{"multiple blocks", random, 4096},
	} {
		t.Run(test.name, func(t *testing.T) {
			encoded := xzData(t, test.data, 64<<10, test.block)
			joined := append(bytes.Clone(encoded), 0, 0, 0, 0)
			joined = append(joined, encoded...)
			r, format, err := Decompress(bytes.NewReader(joined))
			if err != nil || format != "xz" {
				t.Fatal(format, err)
			}
			got, err := io.ReadAll(r)
			if err != nil || !bytes.Equal(got, append(bytes.Clone(test.data), test.data...)) {
				t.Fatal("stream mismatch", err)
			}
			for _, damaged := range [][]byte{encoded[:len(encoded)-1], append(bytes.Clone(encoded), 1)} {
				r, _, err = Decompress(bytes.NewReader(damaged))
				if err == nil {
					_, err = io.Copy(io.Discard, r)
				}
				if err == nil {
					t.Fatal("malformed xz accepted")
				}
			}
			corrupt := bytes.Clone(encoded)
			corrupt[len(corrupt)-1] ^= 1
			r, _, err = Decompress(bytes.NewReader(corrupt))
			if err == nil {
				_, err = io.Copy(io.Discard, r)
			}
			if err == nil {
				t.Fatal("invalid footer accepted")
			}
		})
	}
}

func TestXZDictionaryLimitEveryBlock(t *testing.T) {
	for _, later := range []bool{false, true} {
		t.Run(map[bool]string{false: "first", true: "later"}[later], func(t *testing.T) {
			encoded := xzData(t, bytes.Repeat([]byte("x"), 128), 64<<10, 64)
			offset := 12
			if later {
				g := &xzGuard{input: bytes.NewReader(encoded), limit: 1 << 20}
				buffer := make([]byte, 1)
				for g.blocks < 2 {
					if _, err := g.Read(buffer); err != nil {
						t.Fatal(err)
					}
				}
				offset = len(encoded) - g.input.(*bytes.Reader).Len() - len(g.queued) - 1
			}
			headerSize := (int(encoded[offset]) + 1) * 4
			// one LZMA2 filter with a 4GiB dictionary, rejected before allocation
			encoded[offset+4] = 40
			binary.LittleEndian.PutUint32(encoded[offset+headerSize-4:], crc32.ChecksumIEEE(encoded[offset:offset+headerSize-4]))
			ctx := memory.WithBudget(context.Background(), memory.Budget{Total: 48 * memory.MiB})
			r, _, err := NewDecoder().Decompress(ctx, bytes.NewReader(encoded))
			if err == nil {
				_, err = io.Copy(io.Discard, r)
			}
			if err == nil || !strings.Contains(err.Error(), "dictionary exceeds memory limit") {
				t.Fatal("oversized dictionary not rejected", err)
			}
		})
	}
}

func TestXZLevelsAndBudget(t *testing.T) {
	for level := 1; level <= 9; level++ {
		dictionary, err := xzDictionary(level)
		if err != nil || dictionary != (64<<10)<<(level-1) {
			t.Fatal(level, dictionary, err)
		}
	}
	for _, level := range []int{0, 10} {
		if _, err := Compress(io.Discard, "xz", level); err == nil {
			t.Fatal("invalid level accepted")
		}
	}
	b, err := memory.Resolve("48MiB", 2, 2, func() (int64, error) { t.Fatal("probe called"); return 0, nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = Create(context.Background(), Options{Compression: "xz", Level: 9, Budget: b}); err == nil || !strings.Contains(err.Error(), "larger budget") {
		t.Fatal("insufficient xz budget accepted", err)
	}
	file, pw := makeTestArchive(t, "xz", "encb")
	plain := filepath.Join(t.TempDir(), "plaintext")
	format, err := DecryptFile(context.Background(), file, plain, pw)
	if err != nil || format != "xz" {
		t.Fatal(format, err)
	}
	t.Setenv("PATH", "")
	output := filepath.Join(t.TempDir(), "recovered")
	if _, format, err = Extract(context.Background(), ExtractOptions{Input: plain, Output: output}); err != nil || format != "xz" {
		t.Fatal("plaintext signature detection failed", format, err)
	}
	if _, err = os.Stat(filepath.Join(output, Stem(file), "docs", "file")); err != nil {
		t.Fatal(err)
	}
}
