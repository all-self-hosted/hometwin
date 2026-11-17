package predict

import (
	"math"
	"testing"
	"time"

	"hometwin/internal/home"
)

func TestETAMatchesSimulation(t *testing.T) {
	r := RoomFor(13.7) // the master bedroom
	eta, ok := r.ETA(28, 24, 30, "cool")
	if !ok {
		t.Fatal("cooling 28 → 24 should get there")
	}
	// run the same model with small steps and see when it gets there
	temp, s := 28.0, 0.0
	for temp > 24.06 && s < 7200 {
		temp = r.Step(temp, 1, 30, true, "cool", 24)
		s++
	}
	if math.Abs(s-eta) > 0.03*eta+2 {
		t.Fatalf("ETA %.0f s, simulated %.0f s", eta, s)
	}
	if eta < 8*60 || eta > 20*60 {
		t.Fatalf("a 14 m² bedroom from 28 to 24 ℃ in %.1f min is not plausible", eta/60)
	}
}

func TestETAEdges(t *testing.T) {
	r := RoomFor(15)
	if eta, ok := r.ETA(23, 24, 30, "cool"); !ok || eta != 0 {
		t.Fatalf("already below target: %v %v", eta, ok)
	}
	if _, ok := r.ETA(28, 24, 30, "fan"); ok {
		t.Fatal("fan mode does not cool")
	}
	// heating a cold room
	if eta, ok := r.ETA(16, 22, 8, "heat"); !ok || eta <= 0 {
		t.Fatalf("heating: %v %v", eta, ok)
	}
	// the AC cannot cool below where it balances the outside
	if _, ok := r.ETA(28, 14.5, 35, "cool"); ok {
		t.Fatal("14.5 ℃ is below the balance point")
	}
}

func TestThermostatHolds(t *testing.T) {
	r := RoomFor(15)
	temp := 26.0
	for i := 0; i < 3*3600; i++ {
		temp = r.Step(temp, 1, 32, true, "cool", 24)
	}
	if math.Abs(temp-24) > 0.1 {
		t.Fatalf("held at %.2f, want 24", temp)
	}
	// off, the room drifts toward the outside
	for i := 0; i < 3*3600; i++ {
		temp = r.Step(temp, 1, 32, false, "cool", 24)
	}
	if temp < 26 {
		t.Fatalf("off for 3 h at 32 ℃ outside, still %.2f", temp)
	}
}

func TestEstimatorSmoothsNoise(t *testing.T) {
	e := NewEstimator(home.Sample())
	now := time.Now()
	for i, v := range []float64{25, 25.4, 24.7, 25.2, 24.9, 25.1} {
		e.Observe("living", v, now.Add(time.Duration(i)*5*time.Second))
	}
	got, _, ok := e.Temp("living")
	if !ok || math.Abs(got-25) > 0.25 {
		t.Fatalf("estimate %.2f", got)
	}
	e.Observe("outdoors", 18, now)
	if _, out, _ := e.Temp("living"); out != 18 {
		t.Fatalf("outdoor %v", out)
	}
}
