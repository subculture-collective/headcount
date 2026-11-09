package detect

import (
	"math"
	"slices"
)

// EngagementFit describes a robust log(rate + 0.1) versus log(viewers) fit.
// SD is the residual spread of the retained observations, not model accuracy.
type EngagementFit struct {
	A     float64 `json:"a"`
	Slope float64 `json:"slope"`
	SD    float64 `json:"sd"`
	N     int     `json:"n"`
}

// FitEngagement starts from median slopes between audience-size bin medians.
// A median absolute deviation filter precedes least squares, rather than using
// the contaminated initial least-squares spread to decide which peers to retain.
// At most 20 bins bound the initial slope calculation for large peer sets.
func FitEngagement(viewers, rates []float64, minN int) (EngagementFit, bool) {
	type point struct{ x, y float64 }
	var points []point
	for i, v := range viewers {
		if i >= len(rates) || !finite(v) || !finite(rates[i]) || v <= 0 || rates[i] < 0 {
			continue
		}
		points = append(points, point{math.Log(v), math.Log(rates[i] + 0.1)})
	}
	if len(points) < max(3, minN) {
		return EngagementFit{}, false
	}
	slices.SortFunc(points, func(a, b point) int {
		if a.x < b.x {
			return -1
		}
		if a.x > b.x {
			return 1
		}
		if a.y < b.y {
			return -1
		}
		if a.y > b.y {
			return 1
		}
		return 0
	})
	nBins := min(20, len(points))
	bins := make([]point, nBins)
	for i := range nBins {
		var xs, ys []float64
		for _, p := range points[i*len(points)/nBins : (i+1)*len(points)/nBins] {
			xs, ys = append(xs, p.x), append(ys, p.y)
		}
		bins[i] = point{Median(xs), Median(ys)}
	}
	var slopes []float64
	for _, a := range bins {
		var fromBin []float64
		for _, b := range bins {
			if b.x != a.x {
				fromBin = append(fromBin, (b.y-a.y)/(b.x-a.x))
			}
		}
		if len(fromBin) > 0 {
			slopes = append(slopes, Median(fromBin))
		}
	}
	slope := 0.0
	if len(slopes) > 0 {
		slope = Median(slopes)
	}
	residuals := make([]float64, len(points))
	for i, p := range points {
		residuals[i] = p.y - slope*p.x
	}
	a := Median(residuals)
	spread := MAD(residuals)
	// Exact lines and majority-identical residuals have no measurable spread.
	if !finite(spread) || spread <= 1e-12 {
		return EngagementFit{}, false
	}
	var kept []point
	for _, p := range points {
		if math.Abs(p.y-a-slope*p.x) <= 2.5*spread {
			kept = append(kept, p)
		}
	}
	if len(kept) < max(3, minN) {
		return EngagementFit{}, false
	}
	// Centering avoids cancellation in the least-squares denominator.
	var mx, my float64
	for _, p := range kept {
		mx += p.x
		my += p.y
	}
	mx /= float64(len(kept))
	my /= float64(len(kept))
	var xx, xy float64
	for _, p := range kept {
		xx += (p.x - mx) * (p.x - mx)
		xy += (p.x - mx) * (p.y - my)
	}
	slope = 0
	if xx > 0 {
		slope = xy / xx
	}
	a = my - slope*mx
	var ss float64
	for _, p := range kept {
		r := p.y - a - slope*p.x
		ss += r * r
	}
	sd := math.Sqrt(ss / float64(len(kept)-2))
	if !finite(sd) || sd <= 1e-12 {
		return EngagementFit{}, false
	}
	return EngagementFit{A: a, Slope: slope, SD: sd, N: len(kept)}, true
}
