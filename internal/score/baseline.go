package score

import (
	"math"

	"git.subcult.tv/subculture-collective/headcount/internal/detect"
	"git.subcult.tv/subculture-collective/headcount/internal/store"
)

// baseline models expected chat engagement for a given audience size:
// log(chatters per 1k + 0.1) = a + slope*log(median viewers). Larger streams have
// proportionally quieter chat, so a fixed ratio would flag every big channel.
type baseline struct {
	ok       bool
	a, slope float64
	sd       float64
}

const (
	minPeers     = 20
	minLangPeers = 15
)

// baselines holds a global fit and per-language fits. Chat culture differs by
// language, so a language with enough peer streams gets its own baseline.
type baselines struct {
	global  *baseline
	byLang  map[string]*baseline
	offsets *offsets
}

func fitBaselines(peers []store.PeerStat) *baselines {
	b := &baselines{global: fitBaseline(peers), byLang: map[string]*baseline{}}
	groups := map[string][]store.PeerStat{}
	for _, p := range peers {
		if p.Language != "" {
			groups[p.Language] = append(groups[p.Language], p)
		}
	}
	for lang, g := range groups {
		if len(g) < minLangPeers {
			continue
		}
		if fit := fitBaselineMin(g, minLangPeers); fit.ok {
			b.byLang[lang] = fit
		}
	}
	if b.global.ok {
		b.fitOffsets(peers)
	}
	return b
}

// For returns the baseline for a language and whether it is language-specific.
func (b *baselines) For(lang string) (*baseline, bool) {
	if l, ok := b.byLang[lang]; ok {
		return l, true
	}
	return b.global, false
}

func (b *baselines) ok() bool { return b != nil && b.global != nil && b.global.ok }

func fitBaseline(peers []store.PeerStat) *baseline { return fitBaselineMin(peers, minPeers) }

func fitBaselineMin(peers []store.PeerStat, minN int) *baseline {
	var viewers, rates []float64
	for _, p := range peers {
		viewers = append(viewers, p.MedianViewers)
		rates = append(rates, p.ChattersPerK)
	}
	fit, ok := detect.FitEngagement(viewers, rates, minN)
	return &baseline{ok: ok, a: fit.A, slope: fit.Slope, sd: fit.SD}
}

func (b *baseline) expected(viewers float64) float64 {
	return math.Exp(b.a+b.slope*math.Log(viewers)) - 0.1
}

func (b *baseline) z(viewers, perK float64) float64 {
	return (math.Log(perK+0.1) - (b.a + b.slope*math.Log(viewers))) / b.sd
}
