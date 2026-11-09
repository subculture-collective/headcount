package api

import (
	"math"
	"sort"

	"git.subcult.tv/subculture-collective/headcount/internal/detect"
)

// engagementFit uses the same robust fitting procedure as the scorer, but
// describes only the selected displayed population, one stream per channel.
type engagementFit = detect.EngagementFit

const (
	minFitGlobal = 20
	minFitLang   = 15
)

func fitEngagement(viewers, perK []float64, minN int) (engagementFit, bool) {
	return detect.FitEngagement(viewers, perK, minN)
}

// onsetPartner is another channel's spike onsets inside a stream's window.
type onsetPartner struct {
	ChannelID      string  `json:"channel_id"`
	Login          string  `json:"login"`
	DisplayName    string  `json:"display_name"`
	Onsets         []int64 `json:"onsets"` // unix seconds
	OverlapMinutes int     `json:"overlap_minutes"`
	Matches        int     `json:"matches"`  // own onsets with one of this channel's within the tolerance
	Expected       float64 `json:"expected"` // matches expected by chance
	Named          bool    `json:"named"`    // the scorer's best synchronized partner
}

// scoreOnsetPartners counts, for each partner, how many of own's onsets
// (unix seconds) have a partner onset within ±tol minutes, and how many such
// matches chance alone would give. The arithmetic follows detect.Synchrony,
// which works on unix minutes.
func scoreOnsetPartners(own []int64, partners []onsetPartner, tol int) {
	ownMin := make([]int64, len(own))
	for i, t := range own {
		ownMin[i] = t / 60
	}
	for i := range partners {
		p := &partners[i]
		pm := make([]int64, len(p.Onsets))
		for j, t := range p.Onsets {
			pm[j] = t / 60
		}
		sort.Slice(pm, func(a, b int) bool { return pm[a] < pm[b] })
		k, j := 0, 0
		for _, t := range ownMin {
			for j < len(pm) && pm[j] < t-int64(tol) {
				j++
			}
			if j < len(pm) && pm[j] <= t+int64(tol) {
				k++
			}
		}
		p.Matches = k
		if p.OverlapMinutes > 0 {
			p0 := math.Min(1, float64(len(pm)*(2*tol+1))/float64(p.OverlapMinutes))
			p.Expected = math.Round(p0*float64(len(own))*100) / 100
		}
	}
}

// pickOnsetPartners keeps the partners the synchrony detector would test
// (three or more onsets, an hour of overlap), ranks them by how far matches
// exceed chance, and returns at most limit with at least one match. A named
// partner is always kept.
func pickOnsetPartners(partners []onsetPartner, named string, limit int) (picked []onsetPartner, tested int) {
	var eligible []onsetPartner
	for _, p := range partners {
		if p.Login == named && named != "" {
			p.Named = true
			picked = append(picked, p)
			if len(p.Onsets) >= 3 && p.OverlapMinutes >= 60 {
				tested++
			}
			continue
		}
		if len(p.Onsets) < 3 || p.OverlapMinutes < 60 {
			continue
		}
		tested++
		if p.Matches > 0 {
			eligible = append(eligible, p)
		}
	}
	sort.SliceStable(eligible, func(a, b int) bool {
		ea := float64(eligible[a].Matches) - eligible[a].Expected
		eb := float64(eligible[b].Matches) - eligible[b].Expected
		if ea != eb {
			return ea > eb
		}
		return eligible[a].Login < eligible[b].Login
	})
	for _, p := range eligible {
		if len(picked) >= limit {
			break
		}
		picked = append(picked, p)
	}
	return picked, tested
}
