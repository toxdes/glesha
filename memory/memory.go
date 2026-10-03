package memory

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
)

const MiB int64 = 1 << 20
const Minimum int64 = 48 * MiB

type Budget struct {
	Total, Buffer                int64
	HashWorkers, TransferWorkers int
}

func Parse(s string) (int64, error) {
	s = strings.TrimSpace(s)
	for _, u := range []struct {
		s string
		n int64
	}{{"GiB", 1 << 30}, {"MiB", MiB}, {"KiB", 1 << 10}, {"GB", 1000000000}, {"MB", 1000000}, {"KB", 1000}} {
		if strings.HasSuffix(s, u.s) {
			n, e := strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(s, u.s)), 10, 64)
			if e != nil || n <= 0 || n > (1<<62)/u.n {
				return 0, fmt.Errorf("memory: invalid budget %q", s)
			}
			return n * u.n, nil
		}
	}
	n, e := strconv.ParseInt(s, 10, 64)
	if e != nil || n <= 0 {
		return 0, fmt.Errorf("memory: invalid budget %q", s)
	}
	return n, nil
}
func Available() (int64, error) {
	b, e := os.ReadFile("/proc/meminfo")
	if e != nil {
		return 0, e
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) == 3 && f[0] == "MemAvailable:" {
			n, e := strconv.ParseInt(f[1], 10, 64)
			return n * 1024, e
		}
	}
	return 0, fmt.Errorf("memory: available memory unavailable")
}
func Resolve(max string, hash, transfer int, probe func() (int64, error)) (Budget, error) {
	var total int64
	var e error
	if max != "" {
		total, e = Parse(max)
		if e != nil {
			return Budget{}, e
		}
	} else {
		total, e = probe()
		if e != nil || total <= 0 {
			total = 128 * MiB
		} else {
			total /= 2
		}
	}
	if total < Minimum {
		return Budget{}, fmt.Errorf("memory: budget requires at least 48MiB")
	}
	if hash < 1 || transfer < 1 {
		return Budget{}, fmt.Errorf("memory: worker counts must be positive")
	}
	// reserve runtime, sqlite and compressor working space before sharing buffers
	usable := total - total/4 - 24*MiB
	if usable < 10*MiB {
		usable = 10 * MiB
	}
	if transfer > int(usable/(10*MiB)) {
		transfer = int(usable / (10 * MiB))
	}
	if hash > int(usable/MiB) {
		hash = int(usable / MiB)
	}
	buffer := usable - int64(transfer)*5*MiB - int64(hash)*64*1024
	if buffer < MiB {
		buffer = MiB
	}
	return Budget{total, buffer, hash, transfer}, nil
}
func (b Budget) Apply() { debug.SetMemoryLimit(b.Total) }

type budgetKey struct{}

func WithBudget(ctx context.Context, b Budget) context.Context {
	return context.WithValue(ctx, budgetKey{}, b)
}

func DecoderLimit(ctx context.Context) int64 {
	b, ok := ctx.Value(budgetKey{}).(Budget)
	if !ok {
		return 32 * MiB
	}
	return min(b.Total/4, 64*MiB)
}

func Reserve(b Budget, working int64) (Budget, error) {
	if working <= 24*MiB {
		return b, nil
	}
	usable := b.Total - b.Total/4 - working
	if usable < 10*MiB {
		return Budget{}, fmt.Errorf("memory: compression requires a larger budget or lower compression level")
	}
	b.TransferWorkers = min(b.TransferWorkers, int(usable/(10*MiB)))
	b.HashWorkers = min(b.HashWorkers, int(usable/MiB))
	b.Buffer = usable - int64(b.TransferWorkers)*5*MiB - int64(b.HashWorkers)*64*1024
	b.Buffer = max(b.Buffer, MiB)
	return b, nil
}
