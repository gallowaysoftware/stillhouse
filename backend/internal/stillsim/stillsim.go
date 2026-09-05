// Package stillsim plans a still run before it happens: from a still, a
// charge and a heat setting to the distillate-strength curve, the alcohol
// collected by the time it passes each strength the distiller names, the
// run's length, and the energy it burns.
//
// The model is the pot's mass and energy balance stepped through time.
// Heat in, less what the room takes, raises the charge to its bubble point;
// from there the surplus becomes vapour whose strength is the ethanol–water
// equilibrium applied as many times as the still's geometry, plates and
// dephlegmator amount to. Figures are cited on the constant that carries
// them, to the IBD/CIBD distilling curriculum or to the literature the
// curriculum itself draws on. Where the curriculum gives direction and no
// figure — what harder firing does to the congeners — the plan states the
// direction and stops. The cut by nose stays the distiller's.
//
// The C# reference implementation this was ported from lives in
// ../distillery-sim (src/DistillerySim.Sim, PotStill.cs); its tests hold
// the same curriculum anchors as this package's.
package stillsim

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// Physical constants.
const (
	// EthanolDensityKgPerL at 20 °C — the same figure internal/distilling uses.
	EthanolDensityKgPerL = 0.78934
	// EthanolLatentHeatJPerG: "ethanol ≈ 846 kJ/kg" (CIBD Module 3 — heat transfer).
	EthanolLatentHeatJPerG = 846
	// Specific heats in J/(kg·K): water 4,184 (CIBD Module 3 — heat transfer);
	// ethanol 2,440 (engineering handbook value; the curriculum gives none).
	waterSpecificHeat   = 4184
	ethanolSpecificHeat = 2440
)

// Equilibrium stages the curriculum attributes to still geometry
// (CIBD Module 2 — batch distillation: "tall/narrow → more passive reflux";
// distillation theory: "3 ideal trays ≈ 88 % ABV from an 8 % pot, at 65 %
// efficiency ≈ 2.1 stages"; "R = 2–3 for a floral malt", purifier reflux
// adding stages up to about four).
const (
	StagesSquatPot    = 1.0
	StagesStandardPot = 1.2
	StagesTallPot     = 1.5
	StagesBoilBall    = 0.15
	// TrayEfficiency is the share of an ideal stage a real plate delivers: "50–80 %, 65 % typical".
	TrayEfficiency = 0.65
	// StagesPerUnitReflux is what a dephlegmator's reflux adds per unit of reflux ratio, capped by MaxRefluxStages.
	StagesPerUnitReflux = 0.5
	MaxRefluxStages     = 4.0
)

// Geometry is the pot's shape, which sets its passive reflux.
type Geometry int

const (
	GeometryStandard Geometry = iota
	GeometrySquat
	GeometryTall
)

// HeatingKind is how heat reaches the charge.
type HeatingKind int

const (
	// HeatingElectric: elements in the pot. Heat in is the element rating
	// times the firing setting; the operator's heat control is the setting.
	HeatingElectric HeatingKind = iota
	// HeatingSteam: a jacket or coil on utility steam. Modelled as a rated
	// heat input times the jacket valve setting; the drum and its pressure
	// are the boiler's business, not the still's.
	HeatingSteam
)

// Heating is the heat source as the model needs it.
type Heating struct {
	Kind HeatingKind
	// RatedW is the full-setting heat input: the elements' total rating, or
	// the jacket's duty at working pressure.
	RatedW float64
	// Efficiency is the share of RatedW that reaches the charge at the
	// terminals: 1.0 for immersed elements unless calibration says less.
	Efficiency float64
}

// Still is a still as the model needs it.
type Still struct {
	Name       string
	PotVolumeL float64
	Heating    Heating
	Geometry   Geometry
	BoilBall   bool
	// Plates above the pot, and whether a dephlegmator sits above them. With
	// no dephlegmator the plates run at whatever reflux the column's own
	// losses make; with one, RefluxRatio is a setting.
	Plates       int
	Dephlegmator bool
	// HeatLossW is what the room takes from the pot at the boil; scales
	// with the temperature difference below it. Calibration sets it; the
	// default is an uninsulated stainless pot's.
	HeatLossW float64
	// Calibration, when present, overrides the effective stages and the
	// losses the specification implies.
	Calibration *Calibration
}

// Calibration is what a still's own logged runs said about it.
type Calibration struct {
	Stages     float64
	HeatLossW  float64
	Efficiency float64
	// Runs and On say where the numbers came from, for the plan's basis line.
	Runs int
	On   time.Time
	// RMSStrengthPts is how far the fitted curve sat from the readings, in ABV points.
	RMSStrengthPts float64
}

// Charge is what goes in the pot.
type Charge struct {
	VolumeL float64
	// ABVPct at 20 °C.
	ABVPct float64
	TempC  float64
}

// Settings is how the run is driven.
type Settings struct {
	// Firing in [0,1]: the element dial or the jacket valve, held from the
	// first drop to the end. HeatUpFiring is the setting until then; zero
	// means full, which is how a run is driven.
	Firing       float64
	HeatUpFiring float64
	// RefluxRatio for a dephlegmator, when the still has one; ignored otherwise.
	RefluxRatio float64
	// CutStrengths are the distillate strengths the distiller wants the
	// envelope for, in the order they come: descending.
	CutStrengths []float64
	// StopDistillateABV ends the run: "run to 8–10 %" for feints (CIBD Module 2).
	StopDistillateABV float64
	AmbientC          float64
	// Step is the model's time step; zero takes ten seconds.
	Step time.Duration
}

// Point is one reading along the planned run.
type Point struct {
	At time.Duration
	// DistillateABV is the strength of what is leaving the condenser now —
	// what a parrot would read, cold and corrected.
	DistillateABV float64
	PotABV        float64
	PotTempC      float64
	// Collected so far, in litres of absolute alcohol and in litres of distillate at 20 °C.
	CollectedLAA     float64
	CollectedVolumeL float64
	EnergyKWh        float64
}

// Cut is where the distillate passed a named strength.
type Cut struct {
	ABV          float64
	At           time.Duration
	CollectedLAA float64
	// ShareOfCharge is CollectedLAA over the charge's LAA, in [0,1].
	ShareOfCharge float64
}

// Plan is the run the model expects.
type Plan struct {
	Still     string
	ChargeLAA float64
	// Stages is the effective number of equilibrium stages the plan used.
	Stages float64
	// Basis says where the still's numbers came from — its specification or
	// its calibration — so a plan built on a guess never looks like one built
	// on a run.
	Basis string

	TimeToFirstDrop time.Duration
	RunTime         time.Duration
	Points          []Point
	Cuts            []Cut
	// CollectedLAA at the stop, and its share of the charge.
	CollectedLAA  float64
	ShareOfCharge float64
	EnergyKWh     float64

	// Directions the curriculum gives without a figure, stated as such.
	Directions []string
}

// Presets are the stills on site, as the model needs them.
func Presets() []Still {
	return []Still{
		{
			Name:       "200 L electric, pot",
			PotVolumeL: 200,
			Heating:    Heating{Kind: HeatingElectric, RatedW: 11_000, Efficiency: 1.0},
			Geometry:   GeometryStandard,
			HeatLossW:  defaultHeatLossW(200),
		},
		{
			Name:         "200 L electric, four plates and dephlegmator",
			PotVolumeL:   200,
			Heating:      Heating{Kind: HeatingElectric, RatedW: 11_000, Efficiency: 1.0},
			Geometry:     GeometryStandard,
			Plates:       4,
			Dephlegmator: true,
			HeatLossW:    defaultHeatLossW(200),
		},
		{
			Name:       "30 L electric, pot",
			PotVolumeL: 30,
			Heating:    Heating{Kind: HeatingElectric, RatedW: 2_000, Efficiency: 1.0},
			Geometry:   GeometrySquat,
			HeatLossW:  defaultHeatLossW(30),
		},
	}
}

// defaultHeatLossW is an uninsulated stainless pot's loss at the boil,
// scaled by surface: about a kilowatt for a 200 L pot in a 20 °C room. Not
// a curriculum figure — an engineering estimate, and the first thing
// calibration replaces.
func defaultHeatLossW(potVolumeL float64) float64 {
	return 1000 * math.Pow(potVolumeL/200, 2.0/3.0)
}

// EffectiveStages is the number of equilibrium stages the still amounts to
// at these settings: its geometry, its plates at tray efficiency, and what
// the dephlegmator's reflux adds — or its calibration, which knows better.
func (s Still) EffectiveStages(settings Settings) float64 {
	if s.Calibration != nil && s.Calibration.Stages > 0 {
		return s.Calibration.Stages
	}
	n := StagesStandardPot
	switch s.Geometry {
	case GeometrySquat:
		n = StagesSquatPot
	case GeometryTall:
		n = StagesTallPot
	}
	if s.BoilBall {
		n += StagesBoilBall
	}
	n += float64(s.Plates) * TrayEfficiency
	if s.Dephlegmator && settings.RefluxRatio > 0 {
		n += math.Min(MaxRefluxStages, settings.RefluxRatio*StagesPerUnitReflux)
	}
	return n
}

func (s Still) heatLossW() float64 {
	if s.Calibration != nil && s.Calibration.HeatLossW > 0 {
		return s.Calibration.HeatLossW
	}
	if s.HeatLossW > 0 {
		return s.HeatLossW
	}
	return defaultHeatLossW(s.PotVolumeL)
}

func (s Still) efficiency() float64 {
	if s.Calibration != nil && s.Calibration.Efficiency > 0 {
		return s.Calibration.Efficiency
	}
	if s.Heating.Efficiency > 0 {
		return s.Heating.Efficiency
	}
	return 1
}

func (s Still) basis() string {
	if c := s.Calibration; c != nil && c.Runs > 0 {
		return fmt.Sprintf("calibrated on %d run(s), last %s, fit within %.1f points",
			c.Runs, c.On.Format("2006-01-02"), c.RMSStrengthPts)
	}
	return "estimated from the still's specification; log a run to calibrate"
}

// PlanRun steps a run through and reports what the still will do.
func PlanRun(still Still, charge Charge, settings Settings) (Plan, error) {
	if still.PotVolumeL <= 0 {
		return Plan{}, errors.New("the still needs a pot volume")
	}
	if still.Heating.RatedW <= 0 {
		return Plan{}, errors.New("the still needs a heat rating — the elements' total watts, or the jacket's duty")
	}
	if charge.VolumeL <= 0 || charge.VolumeL > still.PotVolumeL {
		return Plan{}, fmt.Errorf("a charge is between nothing and the pot's %.0f L", still.PotVolumeL)
	}
	if charge.ABVPct <= 0 || charge.ABVPct >= 100 {
		return Plan{}, errors.New("a charge is between 0 and 100 % ABV")
	}
	if settings.Firing <= 0 || settings.Firing > 1 {
		return Plan{}, errors.New("firing is a fraction of full heat, above 0 and up to 1")
	}
	if settings.HeatUpFiring <= 0 {
		settings.HeatUpFiring = 1
	}
	if settings.HeatUpFiring > 1 {
		return Plan{}, errors.New("heat-up firing is a fraction of full heat, up to 1")
	}
	if settings.StopDistillateABV <= 0 {
		settings.StopDistillateABV = 8
	}
	if settings.Step <= 0 {
		settings.Step = 10 * time.Second
	}
	for i := 1; i < len(settings.CutStrengths); i++ {
		if settings.CutStrengths[i] >= settings.CutStrengths[i-1] {
			return Plan{}, errors.New("cut strengths are named in the order they come: descending")
		}
	}

	dt := settings.Step.Seconds()
	n := still.EffectiveStages(settings)
	runW := still.Heating.RatedW * settings.Firing * still.efficiency()
	heatUpW := still.Heating.RatedW * settings.HeatUpFiring * still.efficiency()
	lossW := still.heatLossW()
	// A dephlegmator sends R parts back down for every part that goes on to
	// the condenser, and every part was boiled: the product rate is the
	// boil-up over 1 + R (CIBD Module 2 — distillation theory: R = L/D).
	refluxDivisor := 1.0
	if still.Dephlegmator && settings.RefluxRatio > 0 {
		refluxDivisor = 1 + settings.RefluxRatio
	}

	ethanol, water := massesOf(charge.VolumeL, charge.ABVPct)
	chargeLAA := ethanol / EthanolDensityKgPerL
	temp := charge.TempC
	if temp <= 0 {
		temp = 20
	}
	boil := bubblePointC(charge.ABVPct)

	plan := Plan{
		Still:     still.Name,
		ChargeLAA: chargeLAA,
		Stages:    n,
		Basis:     still.basis(),
		Directions: []string{
			"Harder firing carries heavier congeners into the hearts and brings the transitions sooner and sharper (CIBD Module 2 — batch distillation). The curriculum gives no figure, so this plan shows strength and time only.",
			"A shell-and-tube condenser removes sulphur that a worm tub keeps (CIBD Module 2 — distillation theory); not modelled.",
		},
	}

	var collectedEthanol, collectedWater, energyJ float64
	var elapsed time.Duration
	nextCut := 0
	firstDrop := false
	// What the parrot reads: held between boil-off steps, since the pot
	// spends part of its heat catching up with a bubble point that rises as
	// it weakens, and the stream does not stop for that.
	distillateABV := 0.0
	const maxRun = 24 * time.Hour

	for elapsed < maxRun {
		heatInW := runW
		if !firstDrop {
			heatInW = heatUpW
		}
		lossNow := lossW * math.Max(0, temp-settings.AmbientC) / math.Max(1, boil-settings.AmbientC)
		q := (heatInW - lossNow) * dt
		energyJ += heatInW * dt

		potABV := abvOf(ethanol, water)
		boil = bubblePointC(potABV)
		boiledOff := false

		// Whatever the pot needs to reach its bubble point comes first — it
		// rises as the pot weakens — and the rest becomes vapour.
		cap := water*waterSpecificHeat + ethanol*ethanolSpecificHeat
		needed := math.Max(0, (boil-temp)*cap)
		if q <= needed {
			temp += q / cap
		} else {
			temp = boil
			q -= needed
			boiledOff = true
			distillateABV = stages(potABV, n)
			latent := latentHeatJPerG(distillateABV) * 1000 // J/kg
			massKg := q / latent / refluxDivisor
			wy := massFraction(distillateABV)
			e := math.Min(massKg*wy, ethanol)
			w := math.Min(massKg-e, water)
			ethanol -= e
			water -= w
			collectedEthanol += e
			collectedWater += w
			if !firstDrop {
				firstDrop = true
				plan.TimeToFirstDrop = elapsed
			}
		}

		elapsed += settings.Step
		p := Point{
			At:               elapsed,
			DistillateABV:    distillateABV,
			PotABV:           potABV,
			PotTempC:         temp,
			CollectedLAA:     collectedEthanol / EthanolDensityKgPerL,
			CollectedVolumeL: volumeL(collectedEthanol, collectedWater),
			EnergyKWh:        energyJ / 3.6e6,
		}
		// Keep the curve at a readable density: a point a minute.
		if elapsed%time.Minute == 0 || len(plan.Points) == 0 {
			plan.Points = append(plan.Points, p)
		}

		if boiledOff {
			for nextCut < len(settings.CutStrengths) && distillateABV <= settings.CutStrengths[nextCut] {
				plan.Cuts = append(plan.Cuts, Cut{
					ABV:           settings.CutStrengths[nextCut],
					At:            elapsed,
					CollectedLAA:  p.CollectedLAA,
					ShareOfCharge: p.CollectedLAA / chargeLAA,
				})
				nextCut++
			}
			if distillateABV <= settings.StopDistillateABV || ethanol+water <= 0.001 {
				plan.Points = append(plan.Points, p)
				break
			}
		}
	}

	if !firstDrop {
		return Plan{}, errors.New("the still never boiled: the heat in does not cover the losses — check the rating, the firing and the insulation")
	}
	if elapsed >= maxRun {
		return Plan{}, errors.New("the run did not reach its stop strength in a day — check the firing")
	}

	plan.RunTime = elapsed
	plan.CollectedLAA = collectedEthanol / EthanolDensityKgPerL
	plan.ShareOfCharge = plan.CollectedLAA / chargeLAA
	plan.EnergyKWh = energyJ / 3.6e6
	return plan, nil
}
