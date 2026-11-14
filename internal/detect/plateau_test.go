package detect

import (
	"math"
	"testing"
)

// steppedHold climbs in steps to target, holds it exactly for hold minutes,
// then drops to a small residual audience.
func steppedHold(n, hold int, target float64) []float64 {
	v := make([]float64, n)
	for i := range v {
		switch {
		case i < 20:
			v[i] = math.Round(target * float64(i/4+1) / 6)
		case i < 20+hold:
			v[i] = target
		default:
			v[i] = 12
		}
	}
	return v
}

func constant(n int, x float64) []float64 {
	v := make([]float64, n)
	for i := range v {
		v[i] = x
	}
	return v
}

func healthy(n int) []bool {
	h := make([]bool, n)
	for i := range h {
		h[i] = true
	}
	return h
}

func plateau(d StreamData) Component { return ViewerPlateau(d, Prepare(d)) }

func TestViewerPlateauIgnoresOrganicStreams(t *testing.T) {
	for seed := range uint64(5) {
		v := organic(480, seed)
		c := plateau(StreamData{Viewers: v, Chatters: chatFor(v, 2, false), ChatHealthy: healthy(len(v))})
		if !c.Available || c.Strength > 0 {
			t.Fatalf("seed %d: organic stream scored %.2f (%v)", seed, c.Strength, c.Values)
		}
	}
}

func TestViewerPlateauScoresSilentHeldCeiling(t *testing.T) {
	v := steppedHold(240, 160, 2000)
	c := plateau(StreamData{Viewers: v, Chatters: constant(len(v), 0), ChatHealthy: healthy(len(v))})
	if c.Strength < 0.9 {
		t.Fatalf("silent 160-minute hold scored %.2f (%v)", c.Strength, c.Values)
	}
	if c.Values["held_value"] != 2000 || c.Values["longest_hold_min"] != 160 {
		t.Fatalf("wrong hold: %v", c.Values)
	}
}

func TestViewerPlateauLivelyChatExplainsTheHold(t *testing.T) {
	// A capped stream with a real audience behind the cap keeps chatting.
	v := steppedHold(240, 160, 2000)
	c := plateau(StreamData{Viewers: v, Chatters: constant(len(v), 30), ChatHealthy: healthy(len(v))})
	if c.Strength != 0 {
		t.Fatalf("lively chat should explain the hold, got %.2f", c.Strength)
	}
}

func TestViewerPlateauCapWithReturnsAndChat(t *testing.T) {
	// The count sits at a cap for long stretches, dips below and returns.
	// Busy chat means a real audience behind a self-set cap; silence does not.
	v := make([]float64, 420)
	for i := range v {
		v[i] = 1167
		if i%140 >= 130 {
			v[i] = 1100 + float64(i%7)
		}
	}
	busy := plateau(StreamData{Viewers: v, Chatters: constant(len(v), 12), ChatHealthy: healthy(len(v))})
	silent := plateau(StreamData{Viewers: v, Chatters: constant(len(v), 0), ChatHealthy: healthy(len(v))})
	if busy.Strength != 0 || busy.Values["arrivals"] < 3 {
		t.Fatalf("cap with busy chat scored %.2f (%v)", busy.Strength, busy.Values)
	}
	if silent.Strength < 0.9 {
		t.Fatalf("the same cap with silent chat scored only %.2f (%v)", silent.Strength, silent.Values)
	}
}

func TestViewerPlateauTwoLevelHoldScoresTheCeiling(t *testing.T) {
	// A longer hold below the peak must not hide the hold at the peak.
	v := make([]float64, 300)
	for i := range v {
		switch {
		case i < 20:
			v[i] = 100 + 50*float64(i)
		case i < 170:
			v[i] = 1500
		default:
			v[i] = 2000
		}
	}
	c := plateau(StreamData{Viewers: v, Chatters: constant(len(v), 0), ChatHealthy: healthy(len(v))})
	if c.Strength < 0.9 || c.Values["held_value"] != 2000 {
		t.Fatalf("two-level hold scored %.2f (%v)", c.Strength, c.Values)
	}
}

func TestViewerPlateauNeedsTheHoldAtTheCeiling(t *testing.T) {
	v := steppedHold(300, 120, 2000)
	for i := 200; i < 260; i++ {
		v[i] = 2600 + float64(i%9) // later peak well above the hold
	}
	c := plateau(StreamData{Viewers: v, Chatters: constant(len(v), 0), ChatHealthy: healthy(len(v))})
	if c.Strength != 0 {
		t.Fatalf("hold below the stream maximum scored %.2f (%v)", c.Strength, c.Values)
	}
}

func TestViewerPlateauWithoutUsableChat(t *testing.T) {
	v := steppedHold(240, 160, 2000)
	n := len(v)
	unhealthy := make([]bool, n)
	cases := map[string]StreamData{
		"no chat":        {Viewers: v, Chatters: constant(n, math.NaN())},
		"unhealthy chat": {Viewers: v, Chatters: constant(n, 0), ChatHealthy: unhealthy},
	}
	for name, d := range cases {
		c := plateau(d)
		if math.Abs(c.Strength-plateauNoChat) > 1e-9 {
			t.Fatalf("%s: shape-only credit should be %.2f, got %.2f", name, plateauNoChat, c.Strength)
		}
		if q, _ := Combine(map[string]Component{"viewer_plateau": c}, n); q >= 100*SingleCap {
			t.Fatalf("%s: a plateau without chat evidence must stay below the single-family cap, got %.1f", name, q)
		}
	}
}

func TestViewerPlateauRestrictedChatStandsDown(t *testing.T) {
	// Sub-only or emote-only chat explains silence during the hold.
	v := steppedHold(240, 160, 2000)
	c := plateau(StreamData{Viewers: v, Chatters: constant(len(v), 0), ChatHealthy: healthy(len(v)), RestrictedChat: true})
	if c.Strength != 0 || c.Values["hold_restricted_share"] < 0.9 {
		t.Fatalf("restricted chat should stand down, got %.2f (%v)", c.Strength, c.Values)
	}
}

func TestViewerPlateauPartialChatCanRuleOut(t *testing.T) {
	// A few busy chat minutes are enough to rule a hold out, even when most
	// minutes have no chat observation.
	v := steppedHold(240, 160, 2000)
	ch := constant(len(v), math.NaN())
	for i := 30; i < 45; i++ {
		ch[i] = 60 // 30 chatters per 1000 viewers
	}
	c := plateau(StreamData{Viewers: v, Chatters: ch, ChatHealthy: healthy(len(v))})
	if c.Strength != 0 {
		t.Fatalf("busy partial chat should rule the hold out, got %.2f (%v)", c.Strength, c.Values)
	}
	for i := 30; i < 45; i++ {
		ch[i] = 0
	}
	c = plateau(StreamData{Viewers: v, Chatters: ch, ChatHealthy: healthy(len(v))})
	if c.Strength > plateauNoChat+1e-9 || c.Strength < 0.3 {
		t.Fatalf("silent partial chat should get at most shape-only credit, got %.2f", c.Strength)
	}
}

func TestViewerPlateauFloorAndShortHolds(t *testing.T) {
	small := steppedHold(240, 200, 60)
	if c := plateau(StreamData{Viewers: small, Chatters: constant(240, 0), ChatHealthy: healthy(240)}); c.Strength != 0 {
		t.Fatalf("hold under the floor scored %.2f", c.Strength)
	}
	short := steppedHold(240, 30, 2000)
	if c := plateau(StreamData{Viewers: short, Chatters: constant(240, 0), ChatHealthy: healthy(240)}); c.Strength != 0 {
		t.Fatalf("30-minute hold scored %.2f", c.Strength)
	}
}

func TestViewerPlateauGapsAndContext(t *testing.T) {
	v := steppedHold(240, 160, 2000)
	v[60], v[61] = math.NaN(), math.NaN() // a two-minute polling gap does not break the run
	c := plateau(StreamData{Viewers: v, Chatters: constant(240, 0), ChatHealthy: healthy(240)})
	if c.Values["longest_hold_min"] != 160 {
		t.Fatalf("short gap broke the hold: %v", c.Values)
	}
	long := steppedHold(240, 160, 2000)
	long[60], long[61], long[62] = math.NaN(), math.NaN(), math.NaN() // three missing minutes break it
	if c := plateau(StreamData{Viewers: long, Chatters: constant(240, 0), ChatHealthy: healthy(240)}); c.Values["longest_hold_min"] != 117 {
		t.Fatalf("a gap longer than %d minutes should break the hold: %v", plateauMaxGap, c.Values)
	}
	ctx := make([]bool, 240)
	for i := 90; i < 100; i++ {
		ctx[i] = true // observed collaboration splits the hold
	}
	c = plateau(StreamData{Viewers: v, Chatters: constant(240, 0), ChatHealthy: healthy(240), ContextMinutes: ctx})
	if c.Values["longest_hold_min"] >= 160 {
		t.Fatalf("context minutes should break the hold: %v", c.Values)
	}
}

func TestViewerPlateauFeedsAudienceMechanism(t *testing.T) {
	v := steppedHold(240, 160, 2000)
	c := plateau(StreamData{Viewers: v, Chatters: constant(len(v), 0), ChatHealthy: healthy(len(v))})
	m := Mechanisms(map[string]Component{"viewer_plateau": c})
	if !m["audience"].Available || math.Abs(m["audience"].Evidence-Weights["viewer_plateau"]*c.Strength) > 1e-9 {
		t.Fatalf("audience mechanism %+v, plateau %.2f", m["audience"], c.Strength)
	}
	if q, _ := Combine(map[string]Component{"viewer_plateau": c}, 240); q > 100*SingleCap+1e-9 {
		t.Fatalf("a plateau alone must stay under the single-family cap, got %.1f", q)
	}
}

func TestViewerPlateauIgnoresContextMinutesForChat(t *testing.T) {
	// Silent chat during known collaboration or distribution context must
	// not supply quiet-chat evidence for an eligible hold with lively chat.
	n := 360
	v, ch, ctx := constant(n, 2000), constant(n, 0), make([]bool, n)
	for i := range n {
		if i < 240 {
			ctx[i] = true
		} else {
			ch[i] = 60 // 30 chatters per 1000 viewers
		}
	}
	c := plateau(StreamData{Viewers: v, Chatters: ch, ChatHealthy: healthy(n), ContextMinutes: ctx})
	if c.Strength != 0 {
		t.Fatalf("context minutes supplied quiet-chat evidence: %.2f (%v)", c.Strength, c.Values)
	}
}
