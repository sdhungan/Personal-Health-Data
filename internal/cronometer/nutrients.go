package cronometer

// NutritionAmounts holds every nutrient column cronometer_daily_nutrition
// and cronometer_serving share (see schema.sql — the two tables use the
// exact same ~64-column dictionary at daily vs. per-entry granularity).
// Pointer fields distinguish "not tracked by this account" (nil, leaves any
// existing stored value untouched via COALESCE on upsert) from "tracked,
// value is zero" (non-nil) — same convention internal/googlehealth/sync.go
// uses for dailySummary.
//
// Field order matches schema.sql's column order for easy side-by-side
// review. Nutrient IDs below are CONFIRMED against a real get_nutrients
// response (see cmd/cronodump's dump, 2026-07-31) — not guessed.
type NutritionAmounts struct {
	EnergyKcal       *float64
	CaffeineMg       *float64
	WaterG           *float64
	B1Mg             *float64
	B2Mg             *float64
	B3Mg             *float64
	B5Mg             *float64
	B6Mg             *float64
	B12Ug            *float64
	BiotinUg         *float64
	CholineMg        *float64
	FolateUg         *float64
	VitaminAUg       *float64
	VitaminCMg       *float64
	VitaminDIu       *float64
	VitaminEMg       *float64
	VitaminKUg       *float64
	CalciumMg        *float64
	ChromiumUg       *float64
	CopperMg         *float64
	FluorideUg       *float64
	IodineUg         *float64
	IronMg           *float64
	MagnesiumMg      *float64
	ManganeseMg      *float64
	PhosphorusMg     *float64
	PotassiumMg      *float64
	SeleniumUg       *float64
	SodiumMg         *float64
	ZincMg           *float64
	CarbsG           *float64
	FiberG           *float64
	FructoseG        *float64
	GalactoseG       *float64
	GlucoseG         *float64
	LactoseG         *float64
	MaltoseG         *float64
	StarchG          *float64
	SucroseG         *float64
	SugarsG          *float64
	NetCarbsG        *float64
	AddedSugarsG     *float64
	AlluloseG        *float64
	SugarAlcoholG    *float64
	FatG             *float64
	CholesterolMg    *float64
	MonounsaturatedG *float64
	PolyunsaturatedG *float64
	SaturatedG       *float64
	TransFatG        *float64
	Omega3G          *float64
	Omega6G          *float64
	ProteinG         *float64
	CystineG         *float64
	HistidineG       *float64
	IsoleucineG      *float64
	LeucineG         *float64
	LysineG          *float64
	MethionineG      *float64
	PhenylalanineG   *float64
	ThreonineG       *float64
	TryptophanG      *float64
	TyrosineG        *float64
	ValineG          *float64
}

// nutritionSetters maps a Cronometer nutrient ID to the NutritionAmounts
// field it fills. Built as explicit closures rather than reflection to
// match this codebase's plain-Go style (see internal/googlehealth/sync.go).
var nutritionSetters = map[int]func(*NutritionAmounts, float64){
	208:   func(n *NutritionAmounts, v float64) { n.EnergyKcal = &v },
	262:   func(n *NutritionAmounts, v float64) { n.CaffeineMg = &v },
	255:   func(n *NutritionAmounts, v float64) { n.WaterG = &v },
	404:   func(n *NutritionAmounts, v float64) { n.B1Mg = &v },
	405:   func(n *NutritionAmounts, v float64) { n.B2Mg = &v },
	406:   func(n *NutritionAmounts, v float64) { n.B3Mg = &v },
	410:   func(n *NutritionAmounts, v float64) { n.B5Mg = &v },
	415:   func(n *NutritionAmounts, v float64) { n.B6Mg = &v },
	418:   func(n *NutritionAmounts, v float64) { n.B12Ug = &v },
	10004: func(n *NutritionAmounts, v float64) { n.BiotinUg = &v },
	421:   func(n *NutritionAmounts, v float64) { n.CholineMg = &v },
	417:   func(n *NutritionAmounts, v float64) { n.FolateUg = &v },
	320:   func(n *NutritionAmounts, v float64) { n.VitaminAUg = &v },
	401:   func(n *NutritionAmounts, v float64) { n.VitaminCMg = &v },
	324:   func(n *NutritionAmounts, v float64) { n.VitaminDIu = &v },
	323:   func(n *NutritionAmounts, v float64) { n.VitaminEMg = &v },
	430:   func(n *NutritionAmounts, v float64) { n.VitaminKUg = &v },
	301:   func(n *NutritionAmounts, v float64) { n.CalciumMg = &v },
	10003: func(n *NutritionAmounts, v float64) { n.ChromiumUg = &v },
	312:   func(n *NutritionAmounts, v float64) { n.CopperMg = &v },
	313:   func(n *NutritionAmounts, v float64) { n.FluorideUg = &v },
	10005: func(n *NutritionAmounts, v float64) { n.IodineUg = &v },
	303:   func(n *NutritionAmounts, v float64) { n.IronMg = &v },
	304:   func(n *NutritionAmounts, v float64) { n.MagnesiumMg = &v },
	315:   func(n *NutritionAmounts, v float64) { n.ManganeseMg = &v },
	305:   func(n *NutritionAmounts, v float64) { n.PhosphorusMg = &v },
	306:   func(n *NutritionAmounts, v float64) { n.PotassiumMg = &v },
	317:   func(n *NutritionAmounts, v float64) { n.SeleniumUg = &v },
	307:   func(n *NutritionAmounts, v float64) { n.SodiumMg = &v },
	309:   func(n *NutritionAmounts, v float64) { n.ZincMg = &v },
	205:   func(n *NutritionAmounts, v float64) { n.CarbsG = &v },
	291:   func(n *NutritionAmounts, v float64) { n.FiberG = &v },
	212:   func(n *NutritionAmounts, v float64) { n.FructoseG = &v },
	287:   func(n *NutritionAmounts, v float64) { n.GalactoseG = &v },
	211:   func(n *NutritionAmounts, v float64) { n.GlucoseG = &v },
	213:   func(n *NutritionAmounts, v float64) { n.LactoseG = &v },
	214:   func(n *NutritionAmounts, v float64) { n.MaltoseG = &v },
	209:   func(n *NutritionAmounts, v float64) { n.StarchG = &v },
	210:   func(n *NutritionAmounts, v float64) { n.SucroseG = &v },
	269:   func(n *NutritionAmounts, v float64) { n.SugarsG = &v },
	-1205: func(n *NutritionAmounts, v float64) { n.NetCarbsG = &v },
	10009: func(n *NutritionAmounts, v float64) { n.AddedSugarsG = &v },
	10010: func(n *NutritionAmounts, v float64) { n.AlluloseG = &v },
	10007: func(n *NutritionAmounts, v float64) { n.SugarAlcoholG = &v },
	204:   func(n *NutritionAmounts, v float64) { n.FatG = &v },
	601:   func(n *NutritionAmounts, v float64) { n.CholesterolMg = &v },
	645:   func(n *NutritionAmounts, v float64) { n.MonounsaturatedG = &v },
	646:   func(n *NutritionAmounts, v float64) { n.PolyunsaturatedG = &v },
	606:   func(n *NutritionAmounts, v float64) { n.SaturatedG = &v },
	605:   func(n *NutritionAmounts, v float64) { n.TransFatG = &v },
	10001: func(n *NutritionAmounts, v float64) { n.Omega3G = &v },
	10002: func(n *NutritionAmounts, v float64) { n.Omega6G = &v },
	203:   func(n *NutritionAmounts, v float64) { n.ProteinG = &v },
	507:   func(n *NutritionAmounts, v float64) { n.CystineG = &v },
	512:   func(n *NutritionAmounts, v float64) { n.HistidineG = &v },
	503:   func(n *NutritionAmounts, v float64) { n.IsoleucineG = &v },
	504:   func(n *NutritionAmounts, v float64) { n.LeucineG = &v },
	505:   func(n *NutritionAmounts, v float64) { n.LysineG = &v },
	506:   func(n *NutritionAmounts, v float64) { n.MethionineG = &v },
	508:   func(n *NutritionAmounts, v float64) { n.PhenylalanineG = &v },
	502:   func(n *NutritionAmounts, v float64) { n.ThreonineG = &v },
	501:   func(n *NutritionAmounts, v float64) { n.TryptophanG = &v },
	509:   func(n *NutritionAmounts, v float64) { n.TyrosineG = &v },
	510:   func(n *NutritionAmounts, v float64) { n.ValineG = &v },
}

// nutritionAmountsFromIDs fills a NutritionAmounts from a nutrient-ID ->
// amount map, ignoring any ID nutritionSetters doesn't recognize (nutrients
// this schema doesn't track, e.g. Ash or individual carotenoids).
func nutritionAmountsFromIDs(amounts map[int]float64) *NutritionAmounts {
	n := &NutritionAmounts{}
	for id, v := range amounts {
		if set, ok := nutritionSetters[id]; ok {
			set(n, v)
		}
	}
	return n
}

// nutritionAmountsFromScores builds cronometer_daily_nutrition's amounts
// from get_nutrition_scores' "All Targets" category — the consumed total
// for every nutrient the account currently tracks.
func nutritionAmountsFromScores(components []ScoreComponent) *NutritionAmounts {
	amounts := make(map[int]float64, len(components))
	for _, c := range components {
		amounts[c.NutrientID] = c.Amount
	}
	return nutritionAmountsFromIDs(amounts)
}

// nutritionAmountsFromFood builds one cronometer_serving row's amounts from
// a Food's per-100g nutrient profile, scaled to the entry's actual grams.
func nutritionAmountsFromFood(nutrients []FoodNutrient, grams float64) *NutritionAmounts {
	scale := grams / 100.0
	amounts := make(map[int]float64, len(nutrients))
	for _, fn := range nutrients {
		amounts[fn.ID] = fn.Amount * scale
	}
	return nutritionAmountsFromIDs(amounts)
}
