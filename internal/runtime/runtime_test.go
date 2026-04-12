package runtime_test

import (
	"context"
	"testing"
	"time"

	engine "github.com/chargeghost/engine/internal/engine"
	rt "github.com/chargeghost/engine/internal/runtime"
	"github.com/stretchr/testify/assert"
)

func TestRuntime_AccumulatesEnergy(t *testing.T) {
	e := engine.NewEngine(false, 55000.0)
	e.AddConnector(230.0, 32.0, 1)
	e.PlugIn(1)
	err := e.StartSession(1, -1, nil, 0)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	r := rt.NewRuntime(e)
	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()

	<-done

	meter := e.GetEnergyMeter(1)
	// 230V × 32A × 1 phase × 0.5s = 1.022 Wh — expect at least a small accumulation
	assert.Greater(t, meter.Value, 0.5, "energy meter should have accumulated meaningful Wh")
}

func TestRuntime_StopsOnContextCancel(t *testing.T) {
	e := engine.NewEngine(false, 55000.0)
	e.AddConnector(230.0, 32.0, 1)

	ctx, cancel := context.WithCancel(context.Background())
	r := rt.NewRuntime(e)

	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
		// good
	case <-time.After(500 * time.Millisecond):
		t.Fatal("runtime did not stop after context cancel")
	}
}

func TestRuntime_GetLimitSuspendResume(t *testing.T) {
	e := engine.NewEngine(false, 55000.0)
	e.AddConnector(230.0, 32.0, 1)
	e.PlugIn(1)
	_ = e.StartSession(1, -1, nil, 0)

	// Inject a limit of 0 → should trigger EVSE suspension
	zero := 0.0
	e.GetLimit = func(connectorID, transactionID int, voltage float64, phases int, txStart *time.Time) *float64 {
		return &zero
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	r := rt.NewRuntime(e)
	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()
	<-done

	// Connector should be SuspendedEVSE
	c := e.GetConnector(1)
	assert.Equal(t, engine.StateSuspendedEVSE, c.Status)
}
