package pricing

import (
	"strings"
)

const (
	RatesDate         = "2026-10-01"
	DecimalGB         = 1_000_000_000
	BinaryGB          = 1 << 30
	MinimumIABytes    = 128 * 1024
	ArchiveIndexBytes = 32 * 1024
	ArchiveNameBytes  = 8 * 1024
	MonthsPerYear     = 12
)

type rateTable struct {
	Verified string
	B2       float64
	AWS      map[string]map[string]float64
}

func Date() string { return rates.Verified }

// estimates assume unchanged objects, first-band rates and no account discounts
func Monthly(kind, region, class string, size int64) (float64, bool) {
	if kind == "b2" {
		return MonthlyObjects(kind, region, class, size, 1, 0)
	}
	monitored := int64(0)
	if size >= MinimumIABytes {
		monitored = 1
	}
	return MonthlyObjects(kind, region, class, BillableBytes(class, size), 1, monitored)
}

func BillableBytes(class string, size int64) int64 {
	if (class == "STANDARD_IA" || class == "ONEZONE_IA" || class == "GLACIER_IR") && size >= 0 && size < MinimumIABytes {
		return MinimumIABytes
	}
	return size
}

func Annual(monthly float64) float64 { return monthly * MonthsPerYear }

func MonthlyObjects(kind, region, class string, size, objects, monitored int64) (float64, bool) {
	if size < 0 || objects < 0 || monitored < 0 || monitored > objects {
		return 0, false
	}
	if kind == "b2" {
		return float64(size) / DecimalGB * rates.B2, true
	}
	if kind != "aws" {
		return 0, false
	}
	region = strings.ToLower(strings.TrimSpace(region))
	if region == "" {
		region = "us-east-1"
	}
	classes := rates.AWS[region]
	rate, ok := classes[class]
	if !ok {
		return 0, false
	}
	cost := float64(size) / BinaryGB * rate
	if class == "GLACIER" || class == "DEEP_ARCHIVE" || class == "INTELLIGENT_TIERING_AA" || class == "INTELLIGENT_TIERING_DAA" {
		cost += float64(objects) * (float64(ArchiveIndexBytes)/BinaryGB*rate + float64(ArchiveNameBytes)/BinaryGB*classes["STANDARD"])
	}
	if strings.HasPrefix(class, "INTELLIGENT_TIERING") {
		cost += float64(monitored) * classes["MONITORING_PER_OBJECT"]
	}
	return cost, true
}
