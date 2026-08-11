package statistics

import (
	"math"
	"slices"
)

// Summary returns the arithmetic mean, coefficient of variation, and common
// latency percentiles for a sample. Percentiles use nearest-rank interpolation.
func Summary(values []float64) (mean, coefficientOfVariation, p50, p95, p99 float64) {
	if len(values) == 0 {
		return 0, 0, 0, 0, 0
	}
	ordered := slices.Clone(values)
	slices.Sort(ordered)
	for _, value := range ordered {
		mean += value
	}
	mean /= float64(len(ordered))
	if mean != 0 {
		var sumSquares float64
		for _, value := range ordered {
			delta := value - mean
			sumSquares += delta * delta
		}
		coefficientOfVariation = math.Sqrt(sumSquares/float64(len(ordered))) / math.Abs(mean)
	}
	return mean, coefficientOfVariation, percentile(ordered, .50), percentile(ordered, .95), percentile(ordered, .99)
}

func percentile(ordered []float64, percentile float64) float64 {
	if len(ordered) == 1 {
		return ordered[0]
	}
	position := percentile * float64(len(ordered)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return ordered[lower]
	}
	return ordered[lower] + (ordered[upper]-ordered[lower])*(position-float64(lower))
}
