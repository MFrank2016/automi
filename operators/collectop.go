package group

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/vladimirvivien/automi/api"
	autoctx "github.com/vladimirvivien/automi/api/context"
)

// BinaryOp is executor that knows how to run a binary operation (i.e. aggregation, reduction, etc)
type BinaryOp struct {
	ctx         context.Context
	op          api.BinOperation
	state       interface{}
	concurrency int
	input       <-chan interface{}
	output      chan interface{}
	log         *log.Logger
	cancelled   bool
	mutex       sync.RWMutex
}

// NewBinOp creates a new binary operation executor
func NewBinaryOp(ctx context.Context) *BinaryOp {
	// extract logger
	log := autoctx.GetLogger(ctx)

	o := new(BinaryOp)
	o.ctx = ctx
	o.log = log
	o.concurrency = 1
	o.output = make(chan interface{}, 1024)

	o.log.Printf("component initialized")
	return o
}

// SetOperation sets the operation to execute
func (o *BinaryOp) SetOperation(op api.BinOperation) {
	o.op = op
}

// SetInitialState sets an initial value used with the first streamed item
func (o *BinaryOp) SetInitialState(val interface{}) {
	o.state = val
}

// SetConcurrency sets the concurrency level
func (o *BinaryOp) SetConcurrency(concurr int) {
	o.concurrency = concurr
	if o.concurrency < 1 {
		o.concurrency = 1
	}
}

// SetInput sets the input channel for the executor node
func (o *BinaryOp) SetInput(in <-chan interface{}) {
	o.input = in
}

// GetOutput returns the output channel for the executor node
func (o *BinaryOp) GetOutput() <-chan interface{} {
	return o.output
}

// Exec executes the associated operation
func (o *BinaryOp) Exec() (err error) {
	if o.input == nil {
		err = fmt.Errorf("No input channel found")
		return
	}

	// validate p
	if o.concurrency < 1 {
		o.concurrency = 1
	}

	o.log.Print("execution started")

	go func() {
		defer func() {
			o.output <- o.state
			close(o.output)
			o.log.Print("shuttingdown component")
		}()

		var barrier sync.WaitGroup
		wgDelta := o.concurrency
		barrier.Add(wgDelta)

		for i := 0; i < o.concurrency; i++ { // workers
			go func(wg *sync.WaitGroup) {
				defer wg.Done()
				o.doProc(o.ctx)
			}(&barrier)
		}

		wait := make(chan struct{})
		go func() {
			defer close(wait)
			barrier.Wait()
		}()

		select {
		case <-wait:
			if o.cancelled {
				o.log.Printf("component cancelling...")
				return
			}
		case <-o.ctx.Done():
			o.log.Print("binaryOp done.")
			return
		}
	}()
	return nil
}

// doProc is a helper function that executes the operation
func (o *BinaryOp) doProc(ctx context.Context) {
	if o.op == nil {
		o.log.Print("no operation defined for BinaryOp")
		return
	}
	exeCtx, cancel := context.WithCancel(ctx)

	for {
		select {
		// process incoming item
		case item, opened := <-o.input:
			if !opened {
				return
			}

			o.state = o.op.Apply(exeCtx, o.state, item)

			switch val := o.state.(type) {
			case nil:
				continue
			case error, api.ProcError:
				o.log.Print(val)
				continue
			}

		// is cancelling
		case <-ctx.Done():
			o.log.Println("cancelling....")
			o.mutex.Lock()
			cancel()
			o.cancelled = true
			o.mutex.Unlock()
			return
		}
	}
}
