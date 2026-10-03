package archive

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/ulikunitz/xz"
	"github.com/ulikunitz/xz/lzma"

	"glesha/file_io"
	"glesha/memory"
)

// glesha levels control dictionary size, not the external xz preset algorithm
func xzDictionary(level int) (int, error) {
	if level < 1 || level > 9 {
		return 0, fmt.Errorf("archive: xz compression level must be 1..9")
	}
	return 64 << (10 + level - 1), nil
}

func newXZReader(ctx context.Context, input io.Reader) (io.Reader, error) {
	guard := &xzGuard{input: file_io.ContextReader{Ctx: ctx, Reader: input}, limit: memory.DecoderLimit(ctx)}
	return (xz.ReaderConfig{DictCap: lzma.MinDictCap}).NewReader(guard)
}

type xzStage uint8

const (
	xzHeader xzStage = iota
	xzBlock
	xzChunk
	xzIndex
)

// the library's DictCap is a minimum; check every block before it allocates
type xzGuard struct {
	input     io.Reader
	limit     int64
	stage     xzStage
	queued    []byte
	remaining int64
	blockSize int64
	checkSize int64
	blocks    int
}

func (g *xzGuard) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		if len(g.queued) > 0 {
			n := copy(p, g.queued)
			g.queued = g.queued[n:]
			return n, nil
		}
		if g.remaining > 0 {
			n, err := g.input.Read(p[:min(int64(len(p)), g.remaining)])
			g.remaining -= int64(n)
			if err == io.EOF && g.remaining > 0 {
				err = io.ErrUnexpectedEOF
			}
			return n, err
		}
		if err := g.next(); err != nil {
			return 0, err
		}
	}
}

func (g *xzGuard) take(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := io.ReadFull(g.input, b)
	return b, err
}

func (g *xzGuard) next() error {
	switch g.stage {
	case xzHeader:
		b, err := g.take(4)
		if err != nil {
			return err
		}
		if bytes.Equal(b, []byte{0, 0, 0, 0}) {
			g.queued = b
			return nil
		}
		tail, err := g.take(8)
		if err != nil {
			return err
		}
		b = append(b, tail...)
		if !xz.ValidHeader(b) {
			return fmt.Errorf("archive: invalid xz header")
		}
		switch b[7] {
		case xz.None:
			g.checkSize = 0
		case xz.CRC32:
			g.checkSize = 4
		case xz.CRC64:
			g.checkSize = 8
		case xz.SHA256:
			g.checkSize = 32
		default:
			return fmt.Errorf("archive: unsupported xz checksum")
		}
		g.queued, g.stage = b, xzBlock
	case xzBlock:
		b, err := g.take(1)
		if err != nil {
			return err
		}
		if b[0] == 0 {
			g.queued, g.stage = b, xzIndex
			return nil
		}
		g.blocks++
		if g.blocks > 65536 {
			return fmt.Errorf("archive: xz block count exceeds memory limit")
		}
		tail, err := g.take((int(b[0])+1)*4 - 1)
		if err != nil {
			return err
		}
		b = append(b, tail...)
		if err = g.checkDictionary(b); err != nil {
			return err
		}
		g.queued, g.stage, g.blockSize = b, xzChunk, 0
	case xzChunk:
		b, err := g.take(1)
		if err != nil {
			return err
		}
		if b[0] == 0 {
			g.blockSize++
			g.queued, g.remaining, g.stage = b, (-g.blockSize)&3+g.checkSize, xzBlock
			return nil
		}
		header := 2
		if b[0] >= 0x80 {
			header = 4
			if b[0] >= 0xc0 {
				header++
			}
		} else if b[0] != 1 && b[0] != 2 {
			return fmt.Errorf("archive: invalid xz chunk")
		}
		tail, err := g.take(header)
		if err != nil {
			return err
		}
		b = append(b, tail...)
		sizeOffset := 1
		if b[0] >= 0x80 {
			sizeOffset = 3
		}
		g.remaining = int64(binary.BigEndian.Uint16(b[sizeOffset:])) + 1
		g.blockSize += int64(len(b)) + g.remaining
		g.queued = b
	case xzIndex:
		b := []byte{}
		count, err := g.indexInteger(&b)
		if err != nil {
			return err
		}
		if count > 65536 {
			return fmt.Errorf("archive: xz index exceeds memory limit")
		}
		for i := uint64(0); i < count*2; i++ {
			if _, err = g.indexInteger(&b); err != nil {
				return err
			}
		}
		// index padding, CRC32 and stream footer
		tail, err := g.take((-(len(b) + 1) & 3) + 4 + 12)
		if err != nil {
			return err
		}
		g.queued, g.stage = append(b, tail...), xzHeader
	}
	return nil
}

func (g *xzGuard) indexInteger(out *[]byte) (uint64, error) {
	start := len(*out)
	for i := 0; i < 9; i++ {
		b, err := g.take(1)
		if err != nil {
			return 0, err
		}
		*out = append(*out, b[0])
		if b[0]&0x80 == 0 {
			n, _ := binary.Uvarint((*out)[start:])
			return n, nil
		}
	}
	return 0, fmt.Errorf("archive: invalid xz index integer")
}

func (g *xzGuard) checkDictionary(header []byte) error {
	if header[1]&0x3f != 0 {
		return fmt.Errorf("archive: unsupported xz filter chain")
	}
	r := bytes.NewReader(header[2 : len(header)-4])
	for _, flag := range []byte{0x40, 0x80} {
		if header[1]&flag != 0 {
			if _, err := binary.ReadUvarint(r); err != nil {
				return fmt.Errorf("archive: invalid xz block size: %w", err)
			}
		}
	}
	id, err := binary.ReadUvarint(r)
	if err != nil || id != 0x21 {
		return fmt.Errorf("archive: unsupported xz filter")
	}
	size, err := binary.ReadUvarint(r)
	if err != nil || size != 1 {
		return fmt.Errorf("archive: invalid xz filter properties")
	}
	property, err := r.ReadByte()
	if err != nil {
		return err
	}
	dictionary, err := lzma.DecodeDictCap(property)
	if err != nil || dictionary > g.limit {
		return fmt.Errorf("archive: xz dictionary exceeds memory limit (%dMiB); increase --memory-max", g.limit/memory.MiB)
	}
	return nil
}
