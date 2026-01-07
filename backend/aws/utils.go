package aws

import (
	"context"
	"fmt"
	L "glesha/logger"
	"strings"
	"sync"
)

func (aws *AwsBackend) getProgressLine(progress *sync.Map) string {
	// progress[workerId] -> sentBytes int64
	// progress["maxConcurrentJobs"] -> jobs int
	var sb strings.Builder
	cnt := 0
	maxJobsVal, ok := progress.Load("maxConcurrentJobs")
	maxConcurrentJobs := 1
	if ok {
		maxConcurrentJobs = maxJobsVal.(int)
	}
	for id := 1; id <= maxConcurrentJobs; id++ {
		val, ok := progress.Load(id)
		p := uint64(0)
		if ok {
			p = uint64(val.(int64))
		}
		sb.WriteString(fmt.Sprintf("[CN%d: %s]",
			id,
			L.HumanReadableBytes(p, 1),
		))
		if id != maxConcurrentJobs {
			sb.WriteString(" ")
		}
		cnt++
	}
	return sb.String()
}

func EstimateCost(ctx context.Context, size uint64, currency string) (map[AwsStorageClass]float64, error) {
	exchangeRate, err := getExchangeRate(ctx, "USD", currency)
	if err != nil {
		return nil, err
	}
	awsPricingByStorageClass := map[AwsStorageClass]float64{
		AWS_SC_STANDARD:            exchangeRate * 12 * float64(size) * float64(0.023) * float64(1e-9),
		AWS_SC_INTELLIGENT_TIERING: exchangeRate * 12 * float64(size) * float64(0.023) * float64(1e-9),
		AWS_SC_STANDARD_IA:         exchangeRate * 12 * float64(size) * float64(0.0125) * float64(1e-9),
		AWS_SC_ONEZONE_IA:          exchangeRate * 12 * float64(size) * float64(0.01) * float64(1e-9),
		AWS_SC_GLACIER_IR:          exchangeRate * 12 * float64(size) * float64(0.004) * float64(1e-9),
		AWS_SC_GLACIER:             exchangeRate * 12 * float64(size) * float64(0.00099) * float64(1e-9),
		AWS_SC_DEEP_ARCHIVE:        exchangeRate * 12 * float64(size) * float64(0.00099) * float64(1e-9),
	}
	return awsPricingByStorageClass, nil
}

func renderEstimatedCost(
	_ context.Context,
	size uint64,
	costs map[AwsStorageClass]float64,
	activeStorageClass AwsStorageClass,
	currency string) string {
	var sb strings.Builder
	// TODO: maybe use lipgloss/table instead of handwaving this
	emptySpaceHeader := 32
	headerLine := fmt.Sprintf(
		"AWS S3 Storage Class%sStorage cost for %s/year",
		strings.Repeat(" ", emptySpaceHeader),
		L.HumanReadableBytes(size, 2))
	sb.WriteString(fmt.Sprintf("%s\n", L.Line(len(headerLine))))
	sb.WriteString(headerLine)
	sb.WriteString(fmt.Sprintf("\n%s\n", L.Line(len(headerLine))))
	for _, key := range GetAwsStorageClasses() {
		activeMarker := ""
		label := GetStorageClassLabel(key)
		emptySpace := len(headerLine) - len(label) - emptySpaceHeader/4
		if key == activeStorageClass {
			activeMarker = "✓ "
			emptySpace -= 2
		}
		sb.WriteString(
			fmt.Sprintf(
				"%s%s%s%.2f %s\n",
				activeMarker,
				label,
				strings.Repeat(" ", emptySpace),
				costs[key],
				currency))
	}
	sb.WriteString(fmt.Sprintf("%s\n", L.Line(len(headerLine))))
	sb.WriteString("Note: Above storage costs are an approximation based on storage costs for us-east-1 region, it does not include retrieval/deletion costs.\n")
	return sb.String()
}

func (aws *AwsBackend) getOptimalBlockSizeForSize(sizeInBytes int64) int64 {
	const MB int64 = 1024 * 1024
	const GB int64 = 1024 * MB
	if sizeInBytes <= 20*MB {
		return 10 * MB
	}
	if sizeInBytes <= 5*GB {
		return 30 * MB
	}
	if sizeInBytes <= 20*GB {
		return 50 * MB
	}
	// TODO: tweak these parameters for costs/efficiency etc after profiling
	// since 1e4 is the max limit for number of parts
	// max upload size for a single file is limited to 1.5 TB for now
	return 150 * MB
}

func getExchangeRate(_ context.Context, c1 string, c2 string) (float64, error) {
	// TODO: should this even exist here?
	// if yes, then find a way to get costs in Locale's currency
	if c1 == "USD" && c2 == "INR" {
		return float64(85.56), nil
	}
	return -1, fmt.Errorf("getExchangeRate() does not support: %s-%s rate yet", c1, c2)
}
