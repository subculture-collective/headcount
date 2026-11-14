package detect

import "math"

// Plateau thresholds. A Helix count normally changes every few minutes; one
// exact value held for an hour or more is unusual.
const (
	plateauFloor   = 100 // viewers; small counts hold steady naturally
	plateauMaxGap  = 2   // missing minutes a run may span without breaking
	plateauMinHold = 45  // minutes of a run where strength starts
	plateauFullAt  = 120 // minutes of a run for full duration credit
	plateauQuietK  = 5.0 // chatters per 1000 viewers at which quiet credit starts
	plateauSilentK = 1.0 // chatters per 1000 viewers for full quiet credit
	plateauChatMin = 20  // eligible chat minutes for a full chat judgement
	// plateauNoChat is the credit for the shape alone. It keeps a plateau
	// without chat evidence below the single-family cap (0.6 x 0.4 < 0.3).
	plateauNoChat = 0.4
)

// ViewerPlateau looks for a viewer count held at one exact value for a long
// stretch, with almost no observed minutes above it, while chat stays nearly
// silent. Purchased viewers often look like this: a step climb to a number
// that then does not move. A display cap looks similar: Twitch caps channels
// it finds viewbotting, and Partners can cap themselves. A self-set cap hides
// a real audience above it, so the quiet-chat requirement keeps a capped but
// lively stream from scoring, and a hold during mostly restricted chat (where
// silence is expected) stands down. A frozen Helix value can also hold flat;
// that is not checked here.
//
// Every held value is scored separately and the strongest kept, so a long
// hold below the peak cannot hide a shorter hold at the peak.
func ViewerPlateau(d StreamData, p Prepared) Component {
	v := p.Viewers
	if len(v) < 60 || p.Coverage < 0.7 {
		return unavailable("too short")
	}
	longest := map[float64]int{} // held value -> longest run in minutes
	start := map[float64]int{}
	runStart, runEnd, runValue, gap := -1, -1, math.NaN(), 0
	for i, x := range v {
		if contextAt(d, i, i+1) {
			runStart, runValue, gap = -1, math.NaN(), 0
			continue
		}
		if math.IsNaN(x) {
			gap++
			if gap > plateauMaxGap {
				runStart, runValue = -1, math.NaN()
			}
			continue
		}
		gap = 0
		if x == runValue && runStart >= 0 {
			runEnd = i
		} else {
			runStart, runEnd, runValue = i, i, x
		}
		if l := runEnd - runStart + 1; x >= plateauFloor && l > longest[x] {
			longest[x], start[x] = l, runStart
		}
	}
	longestLen, longestValue := 0, 0.0
	for value, l := range longest {
		if l > longestLen || (l == longestLen && value > longestValue) {
			longestLen, longestValue = l, value
		}
	}
	best := Component{Available: true, Values: map[string]float64{"longest_hold_min": float64(longestLen), "held_value": longestValue}}
	for value, l := range longest {
		if l < plateauMinHold {
			continue // too short to earn any duration credit
		}
		c := plateauAt(d, v, value, l, start[value])
		if c.Strength > best.Strength || (c.Strength == best.Strength && best.Strength > 0 && value > best.Values["held_value"]) {
			best = c
		}
	}
	if best.Strength == 0 && longestLen >= plateauMinHold {
		best = plateauAt(d, v, longestValue, longestLen, start[longestValue])
	}
	return best
}

// plateauAt scores one held value whose longest run lasted hold minutes.
func plateauAt(d StreamData, v []float64, value float64, hold, from int) Component {
	var observed, above, held, arrivals, restricted int
	var ratios []float64
	inHold := false
	for i, x := range v {
		// Context minutes are already explained; they must not count toward
		// the ceiling share, the hold or its chat evidence.
		if math.IsNaN(x) {
			continue
		}
		if contextAt(d, i, i+1) {
			inHold = false
			continue
		}
		observed++
		if x > value {
			above++
		}
		at := x == value
		if at {
			held++
			if !inHold {
				arrivals++
			}
			switch {
			case restrictedAt(d, i):
				restricted++
			case i < len(d.Chatters) && !math.IsNaN(d.Chatters[i]) && (i >= len(d.ChatHealthy) || d.ChatHealthy[i]):
				ratios = append(ratios, d.Chatters[i]/value*1000)
			}
		}
		inHold = at
	}
	aboveShare := float64(above) / float64(observed)
	values := map[string]float64{"longest_hold_min": float64(hold), "held_value": value, "above_share": aboveShare,
		"held_minutes": float64(held), "arrivals": float64(arrivals), "hold_start_min": float64(from),
		"hold_chat_minutes": float64(len(ratios)), "hold_restricted_share": float64(restricted) / float64(held)}
	strength := ramp(float64(hold), plateauMinHold, plateauFullAt) * ramp(aboveShare, 0.05, 0.01)
	switch {
	case 2*restricted > held:
		// Restricted chat explains silence, so the shape is not evidence.
		strength = 0
	case len(ratios) > 0:
		perK := Median(ratios)
		values["hold_chatters_per_k"] = perK
		quiet := ramp(perK, plateauQuietK, plateauSilentK)
		if len(ratios) < plateauChatMin {
			// Partial chat can rule a hold out but not fully confirm it.
			quiet = math.Min(quiet, plateauNoChat)
		}
		strength *= quiet
	default:
		// Without chat during the hold, a cap with a real audience cannot be
		// told apart, so the shape alone gets reduced credit.
		strength *= plateauNoChat
	}
	return Component{Available: true, Strength: strength, Values: values}
}
