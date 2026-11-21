package pricing

import (
	"math"
	"testing"
)

func TestProviderStorageEstimates(t *testing.T) {
	for _, test := range []struct {
		name, kind, region, class string
		bytes                     int64
		want                      float64
		known                     bool
	}{
		{"b2", "b2", "", "STANDARD", DecimalGB, .00695, true},
		{"b2 ignores AWS minimum", "b2", "", "STANDARD_IA", 1, .00695 / DecimalGB, true},
		{"standard", "aws", "us-east-1", "STANDARD", BinaryGB, .023, true},
		{"Singapore case", "aws", "AP-SOUTHEAST-1", "STANDARD", BinaryGB, .025, true},
		{"IA minimum", "aws", "us-east-1", "STANDARD_IA", 1, float64(MinimumIABytes) / BinaryGB * .0125, true},
		{"Deep Archive base", "aws", "us-east-1", "DEEP_ARCHIVE", BinaryGB, .00099 + float64(ArchiveIndexBytes)/BinaryGB*.00099 + float64(ArchiveNameBytes)/BinaryGB*.023, true},
		{"tiering small", "aws", "us-east-1", "INTELLIGENT_TIERING", 1, .023 / BinaryGB, true},
		{"tiering monitored", "aws", "us-east-1", "INTELLIGENT_TIERING", BinaryGB, .023 + .0000025, true},
		{"unknown region", "aws", "unknown", "STANDARD", BinaryGB, 0, false},
		{"unknown class", "aws", "us-east-1", "FUTURE_CLASS", BinaryGB, 0, false},
		{"invalid size", "b2", "", "STANDARD", -1, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, known := Monthly(test.kind, test.region, test.class, test.bytes)
			if known != test.known || math.Abs(got-test.want) > 1e-12 {
				t.Fatal(got, known, test.want)
			}
		})
	}
	if math.Abs(Annual(.025)-.025*MonthsPerYear) > 1e-12 {
		t.Fatal("annual conversion")
	}
}

func TestGroupedMinimumSizesAndMonitoring(t *testing.T) {
	group, known := MonthlyObjects("aws", "us-east-1", "STANDARD_IA", 2*MinimumIABytes, 2, 0)
	single, _ := Monthly("aws", "us-east-1", "STANDARD_IA", 1)
	if !known || group != single*2 {
		t.Fatal("per-object minimum lost", group, single)
	}
	if len(rates.AWS) < 30 || rates.AWS["ap-southeast-1"]["DEEP_ARCHIVE"] != .002 {
		t.Fatal("incomplete rate table")
	}
}
