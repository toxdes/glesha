package memory

import (
	"context"
	"errors"
	"testing"
)

func TestConfiguredBudgetNeverProbes(t *testing.T) {
	for _, value := range []string{"48MiB", "128MiB", "512MiB"} {
		t.Run(value, func(t *testing.T) {
			want, err := Parse(value)
			if err != nil {
				t.Fatal(err)
			}
			b, err := Resolve(value, 10000, 10000, func() (int64, error) { t.Fatal("system memory queried"); return 0, nil })
			if err != nil {
				t.Fatal(err)
			}
			if b.Total != want {
				t.Fatal("configured budget changed")
			}
			if b.HashWorkers >= 10000 || b.TransferWorkers >= 10000 || b.Buffer <= 0 {
				t.Fatal("workers unbounded")
			}
		})
	}
	if _, err := Resolve("1MiB", 1, 1, func() (int64, error) { panic("probe") }); err == nil {
		t.Fatal("insufficient budget accepted")
	}
}
func TestAutomaticBudget(t *testing.T) {
	for _, tc := range []struct {
		name      string
		available int64
		err       error
		want      int64
	}{{"half", 512 * MiB, nil, 256 * MiB}, {"fallback", 0, errors.New("unavailable"), 128 * MiB}} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := Resolve("", 2, 2, func() (int64, error) { return tc.available, tc.err })
			if err != nil || b.Total != tc.want {
				t.Fatal(b, err)
			}
		})
	}
}

func TestCompressionReservationBudgetsWorkersAndBuffer(t *testing.T) {
	b, err := Resolve("256MiB", 1000, 1000, func() (int64, error) { t.Fatal("probe called"); return 0, nil })
	if err != nil {
		t.Fatal(err)
	}
	working := 92 * MiB
	adjusted, err := Reserve(b, working)
	if err != nil {
		t.Fatal(err)
	}
	used := adjusted.Total/4 + working + int64(adjusted.TransferWorkers)*5*MiB + int64(adjusted.HashWorkers)*64*1024 + adjusted.Buffer
	if used > adjusted.Total || adjusted.Buffer >= b.Buffer || adjusted.TransferWorkers >= b.TransferWorkers {
		t.Fatal("compression working memory ignored", used, adjusted)
	}
	ctx := WithBudget(context.Background(), adjusted)
	if DecoderLimit(ctx) != 64*MiB {
		t.Fatal("decoder allowance ignored configured budget")
	}
}
