package ocpp_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/chargeghost/engine/internal/ocpp"
	"github.com/stretchr/testify/assert"
)

func TestCommandDispatcher_ExecutesInOrder(t *testing.T) {
	d := ocpp.NewCommandDispatcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	var results []int
	var mu sync.Mutex

	for i := 0; i < 5; i++ {
		n := i
		d.Enqueue(ocpp.OCPPCommand{
			Description: fmt.Sprintf("cmd %d", n),
			Execute: func() error {
				mu.Lock()
				results = append(results, n)
				mu.Unlock()
				return nil
			},
		})
	}

	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	assert.Equal(t, []int{0, 1, 2, 3, 4}, results)
	mu.Unlock()
}

func TestCommandDispatcher_NonBlockingEnqueue(t *testing.T) {
	d := ocpp.NewCommandDispatcher()
	// Don't start Run — channel fills up.
	// Enqueue should not block.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 300; i++ {
			d.Enqueue(ocpp.OCPPCommand{
				Description: "overflow",
				Execute:     func() error { return nil },
			})
		}
		close(done)
	}()

	select {
	case <-done:
		// good — did not block
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Enqueue blocked when channel was full")
	}
}
