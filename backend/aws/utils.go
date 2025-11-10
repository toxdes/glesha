package aws

import (
	"context"
	"fmt"
	L "glesha/logger"
	"sort"
	"strings"
)

func (aws *AwsBackend) getProgressLine(progress map[int][3]int64) string {
	// progress[workerId][0] -> blockId
	// progress[workerId][1] -> sentBytes
	// progress[workerId][2] -> totalBytes
	var sb strings.Builder
	cnt := 0
	n := len(progress)
	var ids []int
	for k := range progress {
		ids = append(ids, k)
	}
	sort.Ints(ids)
	for _, id := range ids {
		if progress[id][1] == progress[id][2] {
			continue
		}
		sb.WriteString(fmt.Sprintf("[CN%d: %s]",
			id,
			L.HumanReadableBytes(uint64(progress[id][1]), 1)))
		if cnt != n-1 {
			sb.WriteString(" ")
		}
		cnt++
	}
	return sb.String()
}

func (aws *AwsBackend) estimateCost(ctx context.Context, size uint64, currency string) (string, error) {
	exchangeRate, err := aws.getExchangeRate(ctx, "USD", currency)
	if err != nil {
		return "", err
	}
	awsStorageCostPerYear := map[string]float64{
		"StandardFrequent":   12 * float64(size) * float64(0.023) * exchangeRate * float64(1e-9),
		"StandardInfrequent": 12 * float64(size) * float64(0.0125) * exchangeRate * float64(1e-9),
		"Express":            12 * float64(size) * float64(0.11) * exchangeRate * float64(1e-9),
		"GlacierFlexible":    12 * float64(size) * float64(0.0037) * exchangeRate * float64(1e-9),
		"GlacierDeepArchive": 12 * float64(size) * float64(0.00099) * exchangeRate * float64(1e-9),
	}

	var sb strings.Builder
	headerLine := fmt.Sprintf("    S3 Storage Class               Cost for %s (per year)", L.HumanReadableBytes(size, 2))
	sb.WriteString(fmt.Sprintf("%s\n", L.Line(len(headerLine))))
	sb.WriteString(headerLine)
	sb.WriteString(fmt.Sprintf("\n%s\n", L.Line(len(headerLine))))
	sb.WriteString(fmt.Sprintf("Standard (Frequent Retrieval)   :   %*.2f %s\n", 10, awsStorageCostPerYear["StandardFrequent"], currency))
	sb.WriteString(fmt.Sprintf("Standard (Infrequent Retrieval) :   %*.2f %s\n", 10, awsStorageCostPerYear["StandardInfrequent"], currency))
	sb.WriteString(fmt.Sprintf("Express (High Performance)      :   %*.2f %s\n", 10, awsStorageCostPerYear["Express"], currency))
	sb.WriteString(fmt.Sprintf("Glacier (Flexible Retrieval)    :   %*.2f %s\n", 10, awsStorageCostPerYear["GlacierFlexible"], currency))
	sb.WriteString(fmt.Sprintf("Glacier (Deep Archive)          :   %*.2f %s", 10, awsStorageCostPerYear["GlacierDeepArchive"], currency))
	sb.WriteString(fmt.Sprintf("\n%s\n", L.Line(len(headerLine))))
	sb.WriteString("Note: Above storage costs are an approximation based on storage costs for us-east-1 region, it does not include retrieval/deletion costs.\n")
	return sb.String(), nil
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

func (aws *AwsBackend) getExchangeRate(_ context.Context, c1 string, c2 string) (float64, error) {
	// TODO: should this even exist here?
	// if yes, then find a way to get costs in Locale's currency
	if c1 == "USD" && c2 == "INR" {
		return float64(85.56), nil
	}
	return -1, fmt.Errorf("getExchangeRate() does not support: %s-%s rate yet", c1, c2)
}
