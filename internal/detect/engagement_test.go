package detect

import (
	"fmt"
	"math"
	"testing"
)

func TestEngagementSummaryPreservesIntermittentChat(t *testing.T) {
	d := StreamData{Viewers: make([]float64, 60), Chatters: make([]float64, 60), Messages: make([]float64, 60)}
	for i := range d.Viewers {
		d.Viewers[i] = 500
		if i%3 == 0 {
			d.Chatters[i], d.Messages[i] = 5, 10
		}
	}
	s, ok := ChatEngagement(d, Prepared{})
	if !ok || s.ChattersPerK != 0 || math.Abs(s.AggregatePerK-10.0/3) > 1e-9 || s.SilentMinutes != 40 || s.EligibleMinutes != 60 || s.Messages != 200 {
		t.Fatalf("intermittent chat lost: %+v, usable=%v", s, ok)
	}
	// Excluded minutes must affect neither summary's numerator nor denominator.
	d.RestrictedMinutes = make([]bool, 60)
	for i := 0; i < 30; i++ {
		d.RestrictedMinutes[i] = true
	}
	s, ok = ChatEngagement(d, Prepared{})
	if !ok || s.EligibleMinutes != 30 || s.SilentMinutes != 20 || s.Messages != 100 || math.Abs(s.AggregatePerK-10.0/3) > 1e-9 {
		t.Fatalf("restriction changed summary population: %+v", s)
	}
	d.Viewers[59] = math.NaN()
	if _, ok = ChatEngagement(d, Prepared{}); ok {
		t.Fatal("accepted fewer than 30 eligible minutes")
	}
}

func TestRobustEngagementFit(t *testing.T) {
	for _, tt := range []struct {
		percent   int
		clustered bool
	}{{0, false}, {5, false}, {20, false}, {30, false}, {30, true}} {
		t.Run(fmt.Sprintf("zero_%d_clustered_%v", tt.percent, tt.clustered), func(t *testing.T) {
			var viewers, rates []float64
			for i := range 100 {
				v := 100 * math.Pow(1.07, float64(i))
				r := math.Exp(3-0.3*math.Log(v)+0.1*float64(i%2*2-1)) - 0.1
				if (!tt.clustered && (i*37)%100 < tt.percent) || (tt.clustered && i < tt.percent) {
					r = 0
				}
				viewers, rates = append(viewers, v), append(rates, r)
			}
			f, ok := FitEngagement(viewers, rates, 20)
			if !ok || math.Abs(f.A-3) > 0.2 || math.Abs(f.Slope+0.3) > 0.03 || f.SD > 0.15 {
				t.Fatalf("%d%% zero peers distorted fit: %+v, ok=%v", tt.percent, f, ok)
			}
		})
	}
	for _, tt := range []struct {
		name           string
		viewers, rates []float64
	}{
		{"nonfinite", []float64{math.Inf(1), 100, 200}, []float64{2, math.Inf(1), 3}},
		{"short rates", []float64{100, 200, 300}, []float64{2}},
		{"no spread", []float64{100, 100, 100}, []float64{0, 0, 0}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := FitEngagement(tt.viewers, tt.rates, 3); ok {
				t.Fatal("invented a reference fit")
			}
		})
	}
}
