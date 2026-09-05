package stillsim

// The generated rows are integers in fixed units; these read them back as
// the floats the model works in. Strength is percent ABV at 20 °C
// throughout, temperature is °C.

// at reads a table at a fractional row, linearly between rows, clamped at
// both ends.
func at(table []int32, row float64) float64 {
	if row <= 0 {
		return float64(table[0])
	}
	last := float64(len(table) - 1)
	if row >= last {
		return float64(table[len(table)-1])
	}
	i := int(row)
	f := row - float64(i)
	return float64(table[i]) + (float64(table[i+1])-float64(table[i]))*f
}

// massFraction is the ethanol mass fraction of a spirit at a strength, in [0,1].
func massFraction(abvPct float64) float64 {
	return at(massFractionBp[:], abvPct*10) / 10_000
}

// densityKgPerL is the density of ethanol–water at 20 °C for an ethanol mass fraction.
func densityKgPerL(massFraction float64) float64 {
	return at(densityCentiGPerL[:], massFraction*1000) / 100_000
}

// abvOf is the strength at 20 °C of a mixture of ethanol and water masses.
func abvOf(ethanolKg, waterKg float64) float64 {
	total := ethanolKg + waterKg
	if total <= 0 {
		return 0
	}
	w := ethanolKg / total
	return w * densityKgPerL(w) / EthanolDensityKgPerL * 100
}

// volumeL is the volume at 20 °C of a mixture of ethanol and water masses.
func volumeL(ethanolKg, waterKg float64) float64 {
	total := ethanolKg + waterKg
	if total <= 0 {
		return 0
	}
	return total / densityKgPerL(ethanolKg/total)
}

// massesOf is the ethanol and water in a volume at a strength.
func massesOf(volumeL, abvPct float64) (ethanolKg, waterKg float64) {
	w := massFraction(abvPct)
	total := volumeL * densityKgPerL(w)
	return total * w, total * (1 - w)
}

// bubblePointC is the temperature at which a liquid of this strength boils at 1 atm.
func bubblePointC(abvPct float64) float64 {
	return at(bubblePointCentiC[:], abvPct*10) / 100
}

// vapourABV is the strength of the vapour one equilibrium stage throws from a
// liquid of this strength. Below the azeotrope the vapour is the richer.
func vapourABV(liquidABVPct float64) float64 {
	return at(vapourABVBp[:], liquidABVPct*10) / 100
}

// latentHeatJPerG is the latent heat of a vapour of this strength, blended by
// mass between water (the steam table's 100 °C row) and ethanol.
func latentHeatJPerG(vapourABVPct float64) float64 {
	w := massFraction(vapourABVPct)
	return (1-w)*float64(steamLatentHeatJPerG[0]) + w*EthanolLatentHeatJPerG
}

// stages applies the equilibrium a fractional number of times: whole passes
// through the table, then a linear share of one more.
func stages(liquidABVPct, n float64) float64 {
	y := liquidABVPct
	whole := int(n)
	for i := 0; i < whole; i++ {
		y = vapourABV(y)
	}
	if f := n - float64(whole); f > 0 {
		y += (vapourABV(y) - y) * f
	}
	return y
}
